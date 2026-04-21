package tfmoduleschema

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsLocalSource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in    string
		local bool
	}{
		{"/abs/path", true},
		{"./rel", true},
		{"../rel", true},
		{".", true},
		{"..", true},
		{".terraform/modules/acm_us_east_1/", true},
		{".hidden", true},
		{"some/path", false},
		{"github.com/org/repo", false},
		{`C:\src\mod`, true},
		{"C:/src/mod", true},
		{`c:\src\mod`, true},
		{`\\server\share\mod`, true},
		{`.\rel`, true},
		{`..\rel`, true},
		{"file:///abs/path", true},
		{"file::/abs/path", true},
		{"git::https://github.com/x/y.git", false},
		{"s3::https://s3.amazonaws.com/b/k.zip", false},
		{"https://example.com/x.tar.gz", false},
		{"http://example.com/x", false},
		{"", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.local, isLocalSource(tc.in), "isLocalSource(%q)", tc.in)
	}
}

func TestLocalSourcePath(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	require.NoError(t, err)

	got, err := localSourcePath("./foo")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(wd, "foo"), got)

	got, err = localSourcePath("file:///tmp/foo")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/foo", got)

	got, err = localSourcePath("file::/tmp/foo")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/foo", got)

	// file:// with explicit "localhost" host — RFC 8089 allows this
	// and it must resolve to the same path as the host-less form.
	got, err = localSourcePath("file://localhost/tmp/foo")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/foo", got)

	// Percent-encoded segments must be decoded.
	got, err = localSourcePath("file:///tmp/has%20space/foo")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/has space/foo", got)

	// Non-local host in a file URI is not resolvable here.
	_, err = localSourcePath("file://someserver/share/foo")
	require.Error(t, err)
}

// TestGetModule_Source_Local exercises the Source field pointing at a
// local directory. The module must be inspected in place without being
// copied into the cache.
func TestGetModule_Source_Local(t *testing.T) {
	t.Parallel()

	// Point at the existing basic testdata module.
	abs, err := filepath.Abs(filepath.Join("testdata", "basic"))
	require.NoError(t, err)

	cacheDir := t.TempDir()
	// Local sources must not invoke the CacheStatus callback:
	// nothing is cached, and reporting "hit" would be misleading.
	var statuses []CacheStatus
	s := NewServer(nil,
		WithCacheDir(cacheDir),
		WithCacheStatusFunc(func(_ Request, st CacheStatus) { statuses = append(statuses, st) }),
	)
	defer s.Cleanup()

	m, err := s.GetModule(context.Background(), Request{Source: abs})
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.Empty(t, statuses, "local sources must not fire CacheStatus")

	// testdata/basic/main.tf declares these variables.
	var names []string
	for _, v := range m.Variables {
		names = append(names, v.Name)
	}
	assert.Contains(t, names, "name")
	assert.Contains(t, names, "location")

	// Local sources must not populate the on-disk cache. A missing
	// directory is the happy path (nothing was written); any other
	// read error is a real failure we want to surface.
	entries, err := os.ReadDir(filepath.Join(cacheDir, "source"))
	if err != nil {
		require.True(t, os.IsNotExist(err), "unexpected ReadDir error: %v", err)
	} else {
		assert.Empty(t, entries, "local sources must not create cache entries")
	}
}

// TestGetModule_Source_Relative exercises a relative path Source.
func TestGetModule_Source_Relative(t *testing.T) {
	t.Parallel()

	s := NewServer(nil, WithCacheDir(t.TempDir()))
	defer s.Cleanup()

	m, err := s.GetModule(context.Background(), Request{Source: "./testdata/basic"})
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.NotEmpty(t, m.Variables)
}

// TestGetModule_Source_FileScheme exercises a file:// URL Source.
func TestGetModule_Source_FileScheme(t *testing.T) {
	t.Parallel()

	abs, err := filepath.Abs(filepath.Join("testdata", "basic"))
	require.NoError(t, err)

	s := NewServer(nil, WithCacheDir(t.TempDir()))
	defer s.Cleanup()

	m, err := s.GetModule(context.Background(), Request{Source: "file://" + abs})
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.NotEmpty(t, m.Variables)
}

// TestGetModule_Source_Remote exercises a remote Source via an httptest
// server serving a tar.gz archive.
func TestGetModule_Source_Remote(t *testing.T) {
	t.Parallel()

	tgz := buildTarGz(t, map[string]string{
		"main.tf": `variable "remote" {}
output "x" { value = 1 }`,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/archive.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(tgz)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cacheDir := t.TempDir()
	s := NewServer(nil, WithCacheDir(cacheDir))
	defer s.Cleanup()

	source := srv.URL + "/archive.tar.gz?archive=tar.gz"
	m, err := s.GetModule(context.Background(), Request{Source: source})
	require.NoError(t, err)
	require.NotNil(t, m)

	var names []string
	for _, v := range m.Variables {
		names = append(names, v.Name)
	}
	assert.Contains(t, names, "remote")

	// Remote source must populate the on-disk cache.
	entries, err := os.ReadDir(filepath.Join(cacheDir, "source"))
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "remote sources must populate cache")
}

// TestGetModule_Source_VersionConstraintRejected: Source + non-concrete
// version is an error.
func TestGetModule_Source_VersionConstraintRejected(t *testing.T) {
	t.Parallel()
	s := NewServer(nil, WithCacheDir(t.TempDir()))
	defer s.Cleanup()

	_, err := s.GetModule(context.Background(), Request{
		Source:  "./testdata/basic",
		Version: "~> 1.0",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSourceWithConstraint)
}

// TestGetModule_Source_ConcreteVersionAllowed: concrete versions are OK
// with Source requests; unlike version constraints, they are accepted
// but do not affect Source resolution or caching (Source-mode caches
// are keyed only by Source and subpath).
func TestGetModule_Source_ConcreteVersionAllowed(t *testing.T) {
	t.Parallel()
	s := NewServer(nil, WithCacheDir(t.TempDir()))
	defer s.Cleanup()

	m, err := s.GetModule(context.Background(), Request{
		Source:  "./testdata/basic",
		Version: "1.2.3",
	})
	require.NoError(t, err)
	require.NotNil(t, m)
}

// TestListSubmodules_Source: submodule listing via Source.
func TestListSubmodules_Source(t *testing.T) {
	t.Parallel()
	s := NewServer(nil, WithCacheDir(t.TempDir()))
	defer s.Cleanup()

	subs, err := s.ListSubmodules(context.Background(), Request{Source: "./testdata/basic"})
	require.NoError(t, err)
	assert.Contains(t, subs, "modules/network")
}

// TestListSubmodules_Source_VersionConstraintRejected: ListSubmodules
// must enforce the same Source + constraint invariant as GetModule.
func TestListSubmodules_Source_VersionConstraintRejected(t *testing.T) {
	t.Parallel()
	s := NewServer(nil, WithCacheDir(t.TempDir()))
	defer s.Cleanup()

	_, err := s.ListSubmodules(context.Background(), Request{
		Source:  "./testdata/basic",
		Version: "~> 1.0",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSourceWithConstraint)
}

// TestGetSubmodule_Source: submodule inspection via Source.
func TestGetSubmodule_Source(t *testing.T) {
	t.Parallel()
	s := NewServer(nil, WithCacheDir(t.TempDir()))
	defer s.Cleanup()

	m, err := s.GetSubmodule(context.Background(), Request{Source: "./testdata/basic"}, "modules/network")
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, "modules/network", m.Path)
}

// TestCacheSourceDir: cache path derivation is deterministic.
func TestCacheSourceDir(t *testing.T) {
	t.Parallel()
	a := cacheSourceDir("/cache", "git::https://example.com/repo.git?ref=v1")
	b := cacheSourceDir("/cache", "git::https://example.com/repo.git?ref=v1")
	c := cacheSourceDir("/cache", "git::https://example.com/repo.git?ref=v2")
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
	assert.Contains(t, a, filepath.Join("/cache", "source"))
}

// TestGetModule_LocalSource_PicksUpEdits: local Source paths must be
// re-inspected on every call, not memoised by the in-memory module
// cache. Remote sources are content-addressed and may be cached; local
// paths are the source of truth on disk and callers expect edits to
// be visible immediately.
func TestGetModule_LocalSource_PicksUpEdits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tf := filepath.Join(dir, "main.tf")
	require.NoError(t, os.WriteFile(tf, []byte(`variable "first" {}`), 0o644))

	s := NewServer(nil, WithCacheDir(t.TempDir()))
	defer s.Cleanup()

	req := Request{Source: dir}
	m1, err := s.GetModule(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, m1.Variables, 1)
	assert.Equal(t, "first", m1.Variables[0].Name)

	// Rewrite the module. A cached lookup would still report "first".
	require.NoError(t, os.WriteFile(tf, []byte(`variable "second" {}`+"\n"+`variable "third" {}`), 0o644))

	m2, err := s.GetModule(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, m2.Variables, 2, "local-source module edits must be visible on next call")
	names := []string{m2.Variables[0].Name, m2.Variables[1].Name}
	assert.ElementsMatch(t, []string{"second", "third"}, names)
}
