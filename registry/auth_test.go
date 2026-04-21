package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBearerTransport_InjectsOnMatchingHost verifies that the
// Authorization header is added only on requests whose URL host
// matches the configured registry host.
func TestBearerTransport_InjectsOnMatchingHost(t *testing.T) {
	t.Parallel()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"modules":[{"versions":[{"version":"1.0.0"}]}]}`))
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	c, err := NewCustom(srv.URL+"/v1/modules", WithBearerToken("sekrit"))
	require.NoError(t, err)
	assert.Equal(t, u.Host, c.Host())

	_, err = c.ListVersions(context.Background(), VersionsRequest{
		Namespace: "n", Name: "m", System: "s",
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer sekrit", gotAuth)
}

// TestBearerTransport_EmptyTokenIsNoOp: installing the option with an
// empty token must not send an Authorization header.
func TestBearerTransport_EmptyTokenIsNoOp(t *testing.T) {
	t.Parallel()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"modules":[{"versions":[]}]}`))
	}))
	defer srv.Close()

	c, err := NewCustom(srv.URL+"/v1/modules", WithBearerToken(""))
	require.NoError(t, err)
	_, err = c.ListVersions(context.Background(), VersionsRequest{
		Namespace: "n", Name: "m", System: "s",
	})
	require.NoError(t, err)
	assert.Empty(t, gotAuth)
}

// TestBearerTransport_StripsOnCrossHostRedirect verifies the token is
// NOT forwarded when the registry redirects to a different host (e.g.
// a signed S3 URL). This is the critical security property.
func TestBearerTransport_StripsOnCrossHostRedirect(t *testing.T) {
	t.Parallel()

	// Second server represents an unrelated host (e.g. object storage).
	var redirectAuth string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"modules":[{"versions":[]}]}`))
	}))
	defer storage.Close()

	// Registry redirects every request to the storage server.
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, storage.URL+r.URL.Path, http.StatusFound)
	}))
	defer registry.Close()

	c, err := NewCustom(registry.URL+"/v1/modules", WithBearerToken("sekrit"))
	require.NoError(t, err)
	_, err = c.ListVersions(context.Background(), VersionsRequest{
		Namespace: "n", Name: "m", System: "s",
	})
	require.NoError(t, err)
	assert.Empty(t, redirectAuth, "token must NOT be forwarded to a different host")
}

// TestBearerTransport_WrapsExistingClient: a caller-supplied
// http.Client must still have its Transport invoked (e.g. for custom
// TLS config) — we wrap it, not replace it.
func TestBearerTransport_WrapsExistingClient(t *testing.T) {
	t.Parallel()

	var authSeen, markerSeen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
		markerSeen = r.Header.Get("X-Marker")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"modules":[{"versions":[]}]}`))
	}))
	defer srv.Close()

	base := &http.Client{Transport: &markerTransport{wrapped: http.DefaultTransport}}
	c, err := NewCustom(srv.URL+"/v1/modules",
		WithHTTPClient(base),
		WithBearerToken("sekrit"),
	)
	require.NoError(t, err)
	_, err = c.ListVersions(context.Background(), VersionsRequest{
		Namespace: "n", Name: "m", System: "s",
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer sekrit", authSeen)
	assert.Equal(t, "yes", markerSeen, "caller transport must still run")
}

// markerTransport is a trivial RoundTripper used above to prove the
// caller's transport chain is preserved when a bearer token is
// installed.
type markerTransport struct{ wrapped http.RoundTripper }

func (m *markerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("X-Marker", "yes")
	if m.wrapped == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return m.wrapped.RoundTrip(req)
}

// TestBearerTransport_SendsOnRedirectBackToSameHost: if a redirect
// stays on the same host, the Authorization header should be
// re-injected (the transport runs on every hop).
func TestBearerTransport_SendsOnRedirectBackToSameHost(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	var secondAuth string
	mux.HandleFunc("/v1/modules/n/m/s/versions", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/v1/modules/n/m/s/versions/alt", http.StatusFound)
	})
	mux.HandleFunc("/v1/modules/n/m/s/versions/alt", func(w http.ResponseWriter, r *http.Request) {
		secondAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"modules":[{"versions":[]}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// httptest servers listen on 127.0.0.1 so same-host redirects work.
	// Confirm.
	assert.True(t, strings.HasPrefix(srv.URL, "http://127.0.0.1:") || strings.HasPrefix(srv.URL, "http://[::1]:"))

	c, err := NewCustom(srv.URL+"/v1/modules", WithBearerToken("sekrit"))
	require.NoError(t, err)
	_, err = c.ListVersions(context.Background(), VersionsRequest{
		Namespace: "n", Name: "m", System: "s",
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer sekrit", secondAuth, "same-host redirect must still carry the bearer")
}
