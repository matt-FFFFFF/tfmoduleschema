package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDiscoveryServer returns an httptest server that responds at
// /.well-known/terraform.json with the given body and content-type.
func newDiscoveryServer(t *testing.T, body, contentType string, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if status != 0 {
			w.WriteHeader(status)
		}
		fmt.Fprint(w, body)
	})
	return httptest.NewServer(mux)
}

func TestFetchDiscovery_RelativeModulesV1(t *testing.T) {
	t.Parallel()
	srv := newDiscoveryServer(t,
		`{"modules.v1":"/api/registry/v1/modules/"}`,
		"application/json", 0)
	defer srv.Close()

	base, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.NoError(t, err)
	assert.Equal(t, srv.URL+"/api/registry/v1/modules", base)
}

func TestFetchDiscovery_AbsoluteModulesV1(t *testing.T) {
	t.Parallel()
	srv := newDiscoveryServer(t,
		`{"modules.v1":"https://modules.example.com/v1/"}`,
		"application/json", 0)
	defer srv.Close()

	base, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.NoError(t, err)
	assert.Equal(t, "https://modules.example.com/v1", base)
}

func TestFetchDiscovery_JSONCharset(t *testing.T) {
	t.Parallel()
	srv := newDiscoveryServer(t,
		`{"modules.v1":"/v1/modules/"}`,
		"application/json; charset=utf-8", 0)
	defer srv.Close()

	base, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.NoError(t, err)
	assert.Equal(t, srv.URL+"/v1/modules", base)
}

// TestFetchDiscovery_Redirect: a redirect before the discovery doc is
// followed, and the modules.v1 relative reference is resolved against
// the FINAL URL (not the originally requested one).
func TestFetchDiscovery_Redirect(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/other/.well-known/terraform.json")
		w.WriteHeader(http.StatusMovedPermanently)
	})
	mux.HandleFunc("/other/.well-known/terraform.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"modules.v1":"v1/modules/"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	base, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.NoError(t, err)
	// "v1/modules/" is a relative reference; per RFC 3986 it is
	// resolved against the FINAL discovery URL, replacing the last
	// path segment. base=/other/.well-known/terraform.json →
	// /other/.well-known/v1/modules.
	assert.Equal(t, srv.URL+"/other/.well-known/v1/modules", base)
}

func TestFetchDiscovery_Non200(t *testing.T) {
	t.Parallel()
	srv := newDiscoveryServer(t, "", "application/json", http.StatusNotFound)
	defer srv.Close()

	_, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDiscoveryFailed)
}

func TestFetchDiscovery_BadContentType(t *testing.T) {
	t.Parallel()
	srv := newDiscoveryServer(t, `{"modules.v1":"/v1/"}`, "text/html", 0)
	defer srv.Close()

	_, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDiscoveryFailed)
}

func TestFetchDiscovery_MalformedJSON(t *testing.T) {
	t.Parallel()
	srv := newDiscoveryServer(t, `not json`, "application/json", 0)
	defer srv.Close()

	_, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDiscoveryFailed)
}

// TestFetchDiscovery_MissingModulesV1: a host advertising only other
// services returns ErrDiscoveryUnsupported.
func TestFetchDiscovery_MissingModulesV1(t *testing.T) {
	t.Parallel()
	srv := newDiscoveryServer(t, `{"providers.v1":"/v1/providers/"}`, "application/json", 0)
	defer srv.Close()

	_, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDiscoveryUnsupported)
}

// TestFetchDiscovery_MixedTypeSiblingKeys: real-world discovery docs
// (TFC/HCP, and privately-hosted registries following their example)
// advertise some services as objects rather than plain URL strings.
// A non-string value for an UNRELATED key must not break modules.v1
// lookup.
func TestFetchDiscovery_MixedTypeSiblingKeys(t *testing.T) {
	t.Parallel()
	body := `{
      "login.v1": {
        "authz": "https://auth.example.com/login",
        "client": "abc",
        "grant_types": ["authz_code"],
        "ports": [10000, 10010],
        "token": "https://auth.example.com/oauth2/token"
      },
      "modules.v1": "/v1/modules/",
      "providers.v1": "/v1/providers/"
    }`
	srv := newDiscoveryServer(t, body, "application/json", 0)
	defer srv.Close()

	base, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.NoError(t, err)
	assert.Equal(t, srv.URL+"/v1/modules", base)
}

// TestFetchDiscovery_ModulesV1NotAString: if modules.v1 itself is not
// a string, we must fail with ErrDiscoveryFailed (the doc is syntactically
// valid JSON but semantically wrong) rather than silently ignoring it.
func TestFetchDiscovery_ModulesV1NotAString(t *testing.T) {
	t.Parallel()
	srv := newDiscoveryServer(t, `{"modules.v1":{"url":"/v1/modules/"}}`, "application/json", 0)
	defer srv.Close()

	_, err := fetchDiscovery(context.Background(), srv.Client(), srv.URL+"/.well-known/terraform.json")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDiscoveryFailed)
}

// TestDiscoverModulesEndpoint_FullURLBypass: when input is already a
// full URL with a non-empty path, discovery is skipped entirely.
func TestDiscoverModulesEndpoint_FullURLBypass(t *testing.T) {
	t.Parallel()
	base, host, err := DiscoverModulesEndpoint(context.Background(), nil,
		"https://registry.example.com/v1/modules/")
	require.NoError(t, err)
	assert.Equal(t, "https://registry.example.com/v1/modules", base)
	assert.Equal(t, "registry.example.com", host)
}

func TestDiscoverModulesEndpoint_FullURLBypass_WithPort(t *testing.T) {
	t.Parallel()
	_, host, err := DiscoverModulesEndpoint(context.Background(), nil,
		"https://registry.internal:8443/v1/modules")
	require.NoError(t, err)
	assert.Equal(t, "registry.internal:8443", host)
}

func TestDiscoverModulesEndpoint_RejectsGarbage(t *testing.T) {
	t.Parallel()
	_, _, err := DiscoverModulesEndpoint(context.Background(), nil, "")
	require.Error(t, err)
	_, _, err = DiscoverModulesEndpoint(context.Background(), nil, "   ")
	require.Error(t, err)
	_, _, err = DiscoverModulesEndpoint(context.Background(), nil, "has space")
	require.Error(t, err)
}

// TestParseInputHost_RejectsMalformedHost rejects bare-host inputs
// that contain characters which would turn them into something other
// than a bare host[:port] (userinfo, fragment, tab, etc.). Without
// these checks, parseInputHost would otherwise construct a malformed
// discovery URL and fail late in fetchDiscovery with a confusing
// error.
func TestParseInputHost_RejectsMalformedHost(t *testing.T) {
	t.Parallel()
	cases := []string{
		"user@registry.example.com",
		"registry.example.com#frag",
		"registry.example.com?x=1",
		"has\ttab",
		":8443",
	}
	for _, in := range cases {
		_, _, err := parseInputHost(in)
		require.Error(t, err, in)
		assert.ErrorIs(t, err, ErrDiscoveryFailed, in)
	}
}

// TestParseInputHost_RejectsNonHTTPSchemes ensures only http/https
// URLs are accepted. Other schemes (ftp, file, etc.) are rejected so
// they can't slip through the full-URL-with-path bypass and reach a
// caller expecting a Terraform-compatible endpoint.
func TestParseInputHost_RejectsNonHTTPSchemes(t *testing.T) {
	t.Parallel()
	cases := []string{
		"ftp://registry.example.com/v1/modules",
		"file:///tmp/modules",
		"gopher://registry.example.com/v1/modules",
	}
	for _, in := range cases {
		_, _, err := parseInputHost(in)
		require.Error(t, err, in)
		assert.ErrorIs(t, err, ErrDiscoveryFailed, in)
	}
}

// TestParseInputHost_AcceptsHTTPWithPath: http:// is accepted for the
// full-URL-with-path bypass form only (this is how tests point at
// httptest.NewServer, and how a user can skip discovery entirely).
func TestParseInputHost_AcceptsHTTPWithPath(t *testing.T) {
	t.Parallel()
	bypass, host, err := parseInputHost("http://127.0.0.1:8080/v1/modules")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8080/v1/modules", bypass)
	assert.Equal(t, "127.0.0.1:8080", host)
}

func TestResolveDiscoveryReference(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base, ref, want string
	}{
		// absolute path refs replace the whole path
		{"https://ex.com/.well-known/terraform.json", "/v1/modules/", "https://ex.com/v1/modules"},
		// relative refs resolve against the base path with the last
		// segment stripped (RFC 3986 §5.3). The discovery URL's last
		// segment is "terraform.json", so a relative ref becomes a
		// sibling of it under /.well-known/.
		{"https://ex.com/.well-known/terraform.json", "v1/modules/", "https://ex.com/.well-known/v1/modules"},
		{"https://ex.com/other/.well-known/terraform.json", "v1/modules/", "https://ex.com/other/.well-known/v1/modules"},
		// absolute URL refs ignore the base entirely
		{"https://ex.com/.well-known/terraform.json", "https://other.example/v1/modules/", "https://other.example/v1/modules"},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.base)
		require.NoError(t, err)
		got, err := resolveDiscoveryReference(u, tc.ref)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "base=%s ref=%s", tc.base, tc.ref)
	}
}
