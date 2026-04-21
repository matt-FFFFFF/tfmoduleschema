package registry

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	goversion "github.com/hashicorp/go-version"
)

// Custom is the Registry implementation for a user-configured module
// registry — a private registry, a public mirror, or a self-hosted
// instance implementing the minimal module registry protocol.
//
// Custom handles both response styles (JSON body "location" and
// X-Terraform-Get header); callers need not know which the server uses.
type Custom struct {
	opts options
	host string
}

// NewCustom constructs a Custom registry client pointing at baseURL.
// baseURL must be a full base including the /v1/modules (or equivalent)
// path — host-only input is the concern of the service-discovery layer,
// not this constructor.
func NewCustom(baseURL string, opts ...Option) (*Custom, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("custom registry: baseURL must not be empty")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("custom registry: parsing baseURL %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("custom registry: baseURL %q must include scheme and host", baseURL)
	}
	// Apply defaults using the supplied baseURL as the default. Options
	// may still override it via WithBaseURL.
	opts2 := applyOptions(strings.TrimRight(baseURL, "/"), opts)
	applyBearer(&opts2, u.Host)
	return &Custom{
		opts: opts2,
		host: u.Host,
	}, nil
}

// BaseURL returns the base URL of this custom registry client.
func (r *Custom) BaseURL() string { return r.opts.baseURL }

// Host returns the network host of the registry (e.g. "registry.example.com"
// or "registry.internal:8443"). This is used by the Server to derive a
// stable on-disk cache directory and — in later commits — to scope
// bearer-token injection.
func (r *Custom) Host() string { return r.host }

// ListVersions returns all versions of the requested module.
func (r *Custom) ListVersions(ctx context.Context, req VersionsRequest) (goversion.Collection, error) {
	return listVersions(ctx, r.opts.httpClient, r.opts.baseURL, req)
}

// ResolveDownload returns the go-getter-compatible source URL for the
// given concrete version. A custom registry may use either the JSON
// body or the X-Terraform-Get header; resolveDownload handles both,
// with a mild preference for the JSON body.
func (r *Custom) ResolveDownload(ctx context.Context, req DownloadRequest) (string, error) {
	return resolveDownload(ctx, r.opts.httpClient, r.opts.baseURL, req, false)
}
