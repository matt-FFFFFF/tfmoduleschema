package tfmoduleschema

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/matt-FFFFFF/tfmoduleschema/registry"
)

// fakeRegistryServer returns a minimal registry server whose /download
// points at the given on-disk source directory (via go-getter's local
// path support) and whose /versions returns a fixed list.
func fakeRegistryServer(t *testing.T, srcDir string, versions []string) *httptest.Server {
	t.Helper()
	abs, err := filepath.Abs(srcDir)
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions"):
			parts := make([]string, 0, len(versions))
			for _, v := range versions {
				parts = append(parts, fmt.Sprintf(`{"version":%q}`, v))
			}
			fmt.Fprintf(w, `{"modules":[{"versions":[%s]}]}`, strings.Join(parts, ","))
		case strings.HasSuffix(r.URL.Path, "/download"):
			// OpenTofu-style body; force the local file getter so
			// go-getter doesn't try to fetch it over HTTP.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"location":"file://%s"}`, abs)
		default:
			http.NotFound(w, r)
		}
	})
	return httptest.NewServer(mux)
}

func newTestServer(t *testing.T, srcDir string, versions []string) (*Server, *httptest.Server) {
	t.Helper()
	srv := fakeRegistryServer(t, srcDir, versions)
	t.Cleanup(srv.Close)

	reg := registry.NewOpenTofu(registry.WithBaseURL(srv.URL))
	s := NewServer(nil,
		WithCacheDir(t.TempDir()),
		WithRegistry(RegistryTypeOpenTofu, reg),
	)
	return s, srv
}

func TestServer_GetModule_EndToEnd(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t, filepath.Join("testdata", "basic"), []string{"0.1.0", "1.0.0", "1.1.0"})

	req := Request{Namespace: "Azure", Name: "example", System: "azurerm"}
	m, err := s.GetModule(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, m.Variables, 5)
	require.Contains(t, m.RequiredProviders, "azurerm")
	require.Contains(t, m.ModuleCalls, "network")
}

func TestServer_VersionResolution(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t, filepath.Join("testdata", "basic"), []string{"0.1.0", "1.0.0", "1.1.0", "2.0.0"})

	// "~> 1.0" should resolve to 1.1.0
	vars, err := s.GetVariables(context.Background(), Request{
		Namespace: "n", Name: "m", System: "s", Version: "~> 1.0",
	})
	require.NoError(t, err)
	assert.Len(t, vars, 5)
}

func TestServer_ListAndGetSubmodule(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t, filepath.Join("testdata", "basic"), []string{"1.0.0"})

	req := Request{Namespace: "n", Name: "m", System: "s"}
	subs, err := s.ListSubmodules(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, []string{"modules/network"}, subs)

	sub, err := s.GetSubmodule(context.Background(), req, "modules/network")
	require.NoError(t, err)
	assert.Equal(t, "modules/network", sub.Path)
	require.Len(t, sub.Variables, 1)
	assert.Equal(t, "name", sub.Variables[0].Name)
}

func TestServer_CacheStatusCallback(t *testing.T) {
	t.Parallel()
	srv := fakeRegistryServer(t, filepath.Join("testdata", "basic"), []string{"1.0.0"})
	defer srv.Close()
	reg := registry.NewOpenTofu(registry.WithBaseURL(srv.URL))

	cacheDir := t.TempDir()
	req := Request{Namespace: "n", Name: "m", System: "s"}

	// First server populates the on-disk cache (miss).
	var firstStatuses []CacheStatus
	s1 := NewServer(nil,
		WithCacheDir(cacheDir),
		WithRegistry(RegistryTypeOpenTofu, reg),
		WithCacheStatusFunc(func(_ Request, st CacheStatus) { firstStatuses = append(firstStatuses, st) }),
	)
	_, err := s1.GetModule(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, []CacheStatus{CacheStatusMiss}, firstStatuses)

	// Second server with the same cacheDir should hit on-disk.
	var secondStatuses []CacheStatus
	s2 := NewServer(nil,
		WithCacheDir(cacheDir),
		WithRegistry(RegistryTypeOpenTofu, reg),
		WithCacheStatusFunc(func(_ Request, st CacheStatus) { secondStatuses = append(secondStatuses, st) }),
	)
	_, err = s2.GetModule(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, []CacheStatus{CacheStatusHit}, secondStatuses)
}

// TestServer_ForceFetch_RefetchesModifiedCache is a regression test for
// issue #6: --force-fetch / WithForceFetch(true) must actually
// re-download the module even when the cache dir on disk already exists
// and has been tampered with by the user. Previously the downloader had
// an early-return when dest existed, which silently defeated
// force-fetch.
//
// The test mutates the on-disk cache entry directly (not the upstream
// source) so it exercises the exact scenario from the issue: a user
// edits the cache dir, then re-runs with --force-fetch and expects the
// original upstream contents to be restored.
func TestServer_ForceFetch_RefetchesModifiedCache(t *testing.T) {
	t.Parallel()
	// Copy the basic fixture into a temp dir so our test owns the
	// upstream source (the local-file getter may symlink the cache
	// entry to this directory, and we want to be free to replace that
	// symlink without affecting testdata).
	srcDir := t.TempDir()
	require.NoError(t, copyDir(filepath.Join("testdata", "basic"), srcDir))

	srv := fakeRegistryServer(t, srcDir, []string{"1.0.0"})
	defer srv.Close()
	reg := registry.NewOpenTofu(registry.WithBaseURL(srv.URL))

	cacheDir := t.TempDir()
	req := Request{Namespace: "n", Name: "m", System: "s"}
	concreteReq := Request{
		Namespace: "n", Name: "m", System: "s", Version: "1.0.0",
		RegistryType: RegistryTypeOpenTofu,
	}
	modDir := cacheModuleDir(cacheDir, concreteReq, "")

	// Populate the cache without force-fetch.
	s1 := NewServer(nil,
		WithCacheDir(cacheDir),
		WithRegistry(RegistryTypeOpenTofu, reg),
	)
	_, err := s1.GetModule(context.Background(), req)
	require.NoError(t, err)

	// Simulate a user tampering with the cache entry itself. go-getter's
	// local-file getter populates modDir as a symlink; replace it with a
	// real directory that looks "modified" so we're testing the cache
	// layer directly, not the symlink indirection.
	require.NoError(t, os.RemoveAll(modDir))
	require.NoError(t, os.MkdirAll(modDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "tampered.tf"), []byte(`# tampered`), 0o644))

	// New server with force-fetch enabled. It must re-resolve and
	// re-fetch the upstream source even though cacheDir already
	// contains an entry for this version.
	var statuses []CacheStatus
	s2 := NewServer(nil,
		WithCacheDir(cacheDir),
		WithRegistry(RegistryTypeOpenTofu, reg),
		WithForceFetch(true),
		WithCacheStatusFunc(func(_ Request, st CacheStatus) { statuses = append(statuses, st) }),
	)
	_, err = s2.GetModule(context.Background(), req)
	require.NoError(t, err)

	// Must have reported a miss, meaning the download path was taken.
	require.Equal(t, []CacheStatus{CacheStatusMiss}, statuses)

	// The tampered file must be gone and the pristine upstream contents
	// restored.
	_, err = os.Stat(filepath.Join(modDir, "tampered.tf"))
	assert.True(t, os.IsNotExist(err), "tampered.tf should have been removed by force-fetch, got %v", err)
	_, err = os.Stat(filepath.Join(modDir, "main.tf"))
	assert.NoError(t, err, "main.tf should be restored in cache after force-fetch")
}

// copyDir recursively copies src into dst (which must already exist).
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func TestServer_InvalidRequest(t *testing.T) {
	t.Parallel()
	s := NewServer(nil, WithCacheDir(t.TempDir()))
	for _, bad := range []Request{
		{Namespace: "", Name: "n", System: "s"},
		{Namespace: "a/b", Name: "n", System: "s"},
		{Namespace: "..", Name: "n", System: "s"},
	} {
		_, err := s.GetModule(context.Background(), bad)
		require.ErrorIs(t, err, ErrInvalidRequest, "%+v", bad)
	}
}
