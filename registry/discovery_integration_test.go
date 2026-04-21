package registry

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise Terraform remote service discovery against live
// public registries. They are network-dependent and therefore skipped
// under `-short`. No authentication is required; the /.well-known
// endpoint is always public even when the registry itself is auth-gated.

var discoveryIntegrationHosts = []struct {
	name      string
	input     string
	wantHost  string
	mustMatch string // substring the resolved base URL must contain
}{
	{
		name:      "OpenTofu",
		input:     "registry.opentofu.org",
		wantHost:  "registry.opentofu.org",
		mustMatch: "/v1/modules",
	},
	{
		name:      "Terraform",
		input:     "registry.terraform.io",
		wantHost:  "registry.terraform.io",
		mustMatch: "/v1/modules",
	},
	{
		name: "ComplianceTF",
		// A real-world custom registry. The modules endpoint itself
		// is auth-gated (401 on list), but discovery is public.
		input:     "registry.compliance.tf",
		wantHost:  "registry.compliance.tf",
		mustMatch: "/v1/modules",
	},
}

func TestDiscoverModulesEndpoint_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for _, tc := range discoveryIntegrationHosts {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			base, host, err := DiscoverModulesEndpoint(ctx, client, tc.input)
			require.NoError(t, err, "discovery against %s must succeed", tc.input)
			assert.Equal(t, tc.wantHost, host, "input host should round-trip")
			assert.True(t, strings.HasPrefix(base, "https://"),
				"resolved base must be absolute HTTPS, got %q", base)
			assert.Contains(t, base, tc.mustMatch,
				"resolved base %q must contain %q", base, tc.mustMatch)
			t.Logf("%s -> %s", tc.input, base)
		})
	}
}

// TestDiscoverModulesEndpoint_Integration_LazyCustomHost: the
// caller-visible Host() returned by LazyCustom (used for cache keying
// and credential lookup) must not require network I/O and must equal
// the INPUT host for every live registry, even when the modules.v1
// endpoint redirects to a different subdomain.
func TestDiscoverModulesEndpoint_Integration_LazyCustomHost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	for _, tc := range discoveryIntegrationHosts {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLazyCustom(tc.input, nil)
			assert.Equal(t, tc.wantHost, l.Host(),
				"LazyCustom.Host must equal input host without network I/O")
		})
	}
}
