package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCustom_RequiresFullBaseURL(t *testing.T) {
	t.Parallel()

	_, err := NewCustom("")
	require.Error(t, err)

	_, err = NewCustom("not a url")
	require.Error(t, err)

	_, err = NewCustom("/path/without/scheme")
	require.Error(t, err)

	// Host without scheme is rejected — that is the service-discovery
	// layer's problem to solve.
	_, err = NewCustom("registry.example.com")
	require.Error(t, err)
}

func TestNewCustom_TrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	c, err := NewCustom("https://registry.example.com/v1/modules/")
	require.NoError(t, err)
	assert.Equal(t, "https://registry.example.com/v1/modules", c.BaseURL())
	assert.Equal(t, "registry.example.com", c.Host())
}

func TestNewCustom_HostWithPort(t *testing.T) {
	t.Parallel()
	c, err := NewCustom("https://registry.internal:8443/v1/modules")
	require.NoError(t, err)
	assert.Equal(t, "registry.internal:8443", c.Host())
}

// TestNewCustom_WithBaseURLOverrideRedrivesHost ensures that when
// WithBaseURL overrides the constructor's baseURL, the Host() value
// (used for cache scoping and bearer-token injection) reflects the
// OVERRIDE, not the original argument. Otherwise auth scope can
// disagree with the URL requests actually go to.
func TestNewCustom_WithBaseURLOverrideRedrivesHost(t *testing.T) {
	t.Parallel()
	c, err := NewCustom(
		"https://original.example.com/v1/modules",
		WithBaseURL("https://override.example.com:8443/api/registry/v1/modules"),
	)
	require.NoError(t, err)
	assert.Equal(t, "https://override.example.com:8443/api/registry/v1/modules", c.BaseURL())
	assert.Equal(t, "override.example.com:8443", c.Host())
}

// TestNewCustom_WithBaseURLOverrideValidated ensures a bad override
// still produces an error rather than being accepted because the
// original argument parsed cleanly.
func TestNewCustom_WithBaseURLOverrideValidated(t *testing.T) {
	t.Parallel()
	_, err := NewCustom(
		"https://original.example.com/v1/modules",
		WithBaseURL("not a url"),
	)
	require.Error(t, err)
}

// TestCustom_Lists exercises a Custom registry against the httptest
// server defined in registry_test.go. Custom uses body-preferred
// download resolution like OpenTofu.
func TestCustom_Lists(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, serverConfig{
		versions:     []string{"1.0.0", "1.1.0", "2.0.0"},
		downloadMode: "body",
		location:     "git::https://example.com/mod.git?ref=v1.1.0",
	})
	defer srv.Close()

	c, err := NewCustom(srv.URL + "/v1/modules")
	require.NoError(t, err)

	vs, err := c.ListVersions(context.Background(), VersionsRequest{
		Namespace: "anton", Name: "mod", System: "aws",
	})
	require.NoError(t, err)
	require.Len(t, vs, 3)

	loc, err := c.ResolveDownload(context.Background(), DownloadRequest{
		Namespace: "anton", Name: "mod", System: "aws", Version: "1.1.0",
	})
	require.NoError(t, err)
	assert.Equal(t, "git::https://example.com/mod.git?ref=v1.1.0", loc)
}

// TestCustom_HeaderStyle: Custom must also accept registries that
// respond in X-Terraform-Get style, not just body style.
func TestCustom_HeaderStyle(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, serverConfig{
		versions:     []string{"1.0.0"},
		downloadMode: "header",
		location:     "git::https://example.com/mod.git?ref=v1.0.0",
	})
	defer srv.Close()

	c, err := NewCustom(srv.URL + "/v1/modules")
	require.NoError(t, err)

	loc, err := c.ResolveDownload(context.Background(), DownloadRequest{
		Namespace: "anton", Name: "mod", System: "aws", Version: "1.0.0",
	})
	require.NoError(t, err)
	assert.Equal(t, "git::https://example.com/mod.git?ref=v1.0.0", loc)
}

// TestCustom_EndpointKey: two Custom registries served from the same
// host but different paths must produce different EndpointKey values
// so the Server can keep their on-disk caches separate. A single
// registry with an empty path must produce EndpointKey == Host.
func TestCustom_EndpointKey(t *testing.T) {
	t.Parallel()

	a, err := NewCustom("https://reg.example.com/teamA/v1/modules")
	require.NoError(t, err)
	b, err := NewCustom("https://reg.example.com/teamB/v1/modules")
	require.NoError(t, err)
	rootA, err := NewCustom("https://reg.example.com")
	require.NoError(t, err)

	assert.Equal(t, a.Host(), b.Host(), "same host must yield same Host()")
	assert.NotEqual(t, a.EndpointKey(), b.EndpointKey(),
		"distinct paths on same host must yield distinct EndpointKey")
	assert.True(t, strings.HasPrefix(a.EndpointKey(), a.Host()+"_"),
		"EndpointKey for pathed input must extend Host with a suffix")
	assert.Equal(t, rootA.Host(), rootA.EndpointKey(),
		"empty path must make EndpointKey identical to Host")
}
