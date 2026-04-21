package tfmoduleschema

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServer_CustomRegistry_EndToEnd: full custom-registry flow using
// the existing fakeRegistryServer helper. Verifies that:
//   - RegistryTypeCustom Requests are routed to the configured custom
//     registry (not to a public one);
//   - the on-disk cache is scoped by host under <cacheDir>/custom/<host>/…
func TestServer_CustomRegistry_EndToEnd(t *testing.T) {
	t.Parallel()
	srv := fakeRegistryServer(t, filepath.Join("testdata", "basic"), []string{"1.0.0"})
	defer srv.Close()

	cacheDir := t.TempDir()
	s := NewServer(nil,
		WithCacheDir(cacheDir),
		WithCustomRegistry(srv.URL+"/v1/modules"),
	)
	defer s.Cleanup()

	req := Request{
		Namespace:    "anton",
		Name:         "mod",
		System:       "aws",
		RegistryType: RegistryTypeCustom,
	}
	m, err := s.GetModule(context.Background(), req)
	require.NoError(t, err)
	require.NotEmpty(t, m.Variables)

	// Confirm the cache lives under custom/<host>/… with the host
	// extracted from srv.URL.
	host := strings.TrimPrefix(srv.URL, "http://")
	host = strings.TrimPrefix(host, "https://")
	customRoot := filepath.Join(cacheDir, "custom", sanitizeHost(host))
	entries, err := os.ReadDir(customRoot)
	require.NoError(t, err, "custom registry cache root should exist at %s", customRoot)
	require.NotEmpty(t, entries)
}

// TestServer_CustomRegistry_NotConfigured: using RegistryTypeCustom
// without WithCustomRegistry must produce a clear error.
func TestServer_CustomRegistry_NotConfigured(t *testing.T) {
	t.Parallel()
	s := NewServer(nil, WithCacheDir(t.TempDir()))
	defer s.Cleanup()

	_, err := s.GetModule(context.Background(), Request{
		Namespace:    "x",
		Name:         "y",
		System:       "z",
		RegistryType: RegistryTypeCustom,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	assert.Contains(t, err.Error(), "custom registry")
}

// TestWithCustomRegistry_BadURL: constructor errors surface as a panic
// at option-application time so misconfiguration is loud.
func TestWithCustomRegistry_BadURL(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		NewServer(nil, WithCustomRegistry(""))
	})
	assert.Panics(t, func() {
		NewServer(nil, WithCustomRegistry("has space"))
	})
}

// TestServer_CustomRegistry_Discovery: when the caller supplies a
// host-only input, WithCustomRegistry performs Terraform remote
// service discovery on first use to resolve the modules.v1 endpoint.
//
// We emulate the discovery + registry protocol on a single httptest
// server and rewrite the registry's http.Client to send the discovery
// request (which the discovery code hardcodes to https://) via the
// test server.
func TestServer_CustomRegistry_Discovery(t *testing.T) {
	t.Parallel()

	abs, err := filepath.Abs(filepath.Join("testdata", "basic"))
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Discovery returns a relative modules.v1 reference — the
		// most common shape in the wild.
		_, _ = io.WriteString(w, `{"modules.v1":"/v1/modules/"}`)
	})
	mux.HandleFunc("/v1/modules/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions"):
			_, _ = io.WriteString(w, `{"modules":[{"versions":[{"version":"1.0.0"}]}]}`)
		case strings.HasSuffix(r.URL.Path, "/download"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"location":"file://`+abs+`"}`)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Build an HTTP client that rewrites https://registry.internal/...
	// requests into the test server. This is the only way to exercise
	// the HTTPS-only discovery path in a unit test.
	rewriteHost := "registry.internal"
	testSrvURL, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &hostRewriteTransport{
		match:  rewriteHost,
		target: testSrvURL,
	}}

	s := NewServer(nil,
		WithCacheDir(t.TempDir()),
		WithHTTPClient(client),
		WithCustomRegistry(rewriteHost),
	)
	defer s.Cleanup()

	req := Request{
		Namespace:    "anton",
		Name:         "mod",
		System:       "aws",
		RegistryType: RegistryTypeCustom,
	}
	m, err := s.GetModule(context.Background(), req)
	require.NoError(t, err)
	require.NotEmpty(t, m.Variables)
}

// TestServer_CustomRegistry_HTTPClientAppliedAfter verifies that
// WithHTTPClient takes effect even when applied AFTER
// WithCustomRegistry. The LazyCustom must be materialised on first
// use so it picks up the final httpClient; otherwise discovery would
// be sent via http.DefaultClient and blow up against the bogus
// https://registry.internal host.
func TestServer_CustomRegistry_HTTPClientAppliedAfter(t *testing.T) {
	t.Parallel()

	abs, err := filepath.Abs(filepath.Join("testdata", "basic"))
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"modules.v1":"/v1/modules/"}`)
	})
	mux.HandleFunc("/v1/modules/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions"):
			_, _ = io.WriteString(w, `{"modules":[{"versions":[{"version":"1.0.0"}]}]}`)
		case strings.HasSuffix(r.URL.Path, "/download"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"location":"file://`+abs+`"}`)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rewriteHost := "registry.internal"
	testSrvURL, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &hostRewriteTransport{
		match:  rewriteHost,
		target: testSrvURL,
	}}

	// Order matters: WithCustomRegistry first, WithHTTPClient LAST.
	s := NewServer(nil,
		WithCacheDir(t.TempDir()),
		WithCustomRegistry(rewriteHost),
		WithHTTPClient(client),
	)
	defer s.Cleanup()

	m, err := s.GetModule(context.Background(), Request{
		Namespace: "anton", Name: "mod", System: "aws",
		RegistryType: RegistryTypeCustom,
	})
	require.NoError(t, err, "late WithHTTPClient must reach the custom registry")
	require.NotEmpty(t, m.Variables)
}

// hostRewriteTransport redirects requests whose Host matches `match`
// onto `target`. Used to let tests exercise code that hardcodes
// https://<host>/ URLs.
type hostRewriteTransport struct {
	match  string
	target *url.URL
}

func (t *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == t.match {
		req = req.Clone(req.Context())
		req.URL.Scheme = t.target.Scheme
		req.URL.Host = t.target.Host
		req.Host = t.target.Host
	}
	return http.DefaultTransport.RoundTrip(req)
}

// TestServer_CustomRegistry_CacheScopingByHost: two distinct custom
// registries serving the same ns/name/system/version must write to
// different on-disk cache paths.
func TestServer_CustomRegistry_CacheScopingByHost(t *testing.T) {
	t.Parallel()
	srvA := fakeRegistryServer(t, filepath.Join("testdata", "basic"), []string{"1.0.0"})
	defer srvA.Close()
	srvB := fakeRegistryServer(t, filepath.Join("testdata", "basic"), []string{"1.0.0"})
	defer srvB.Close()

	cacheDir := t.TempDir()
	req := Request{
		Namespace:    "anton",
		Name:         "mod",
		System:       "aws",
		RegistryType: RegistryTypeCustom,
	}

	// Server A
	sA := NewServer(nil, WithCacheDir(cacheDir), WithCustomRegistry(srvA.URL+"/v1/modules"))
	defer sA.Cleanup()
	_, err := sA.GetModule(context.Background(), req)
	require.NoError(t, err)

	// Server B — same cacheDir, different custom registry
	sB := NewServer(nil, WithCacheDir(cacheDir), WithCustomRegistry(srvB.URL+"/v1/modules"))
	defer sB.Cleanup()
	_, err = sB.GetModule(context.Background(), req)
	require.NoError(t, err)

	customRoot := filepath.Join(cacheDir, "custom")
	entries, err := os.ReadDir(customRoot)
	require.NoError(t, err)
	// Expect two distinct host subdirs.
	require.Len(t, entries, 2, "expected two host-scoped cache subdirs under %s", customRoot)
}
