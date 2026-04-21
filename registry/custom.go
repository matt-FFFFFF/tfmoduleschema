package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
//
// The supplied baseURL is the DEFAULT: options may override it via
// WithBaseURL. When that happens the host used for cache scoping and
// bearer-token injection is derived from the OVERRIDE, so auth scope
// and the actual request URL cannot disagree.
func NewCustom(baseURL string, opts ...Option) (*Custom, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("custom registry: baseURL must not be empty")
	}
	if _, err := parseAndValidateBaseURL(baseURL); err != nil {
		return nil, err
	}
	// Apply defaults using the supplied baseURL as the default. Options
	// may still override it via WithBaseURL.
	opts2 := applyOptions(strings.TrimRight(baseURL, "/"), opts)
	// Re-parse the final base URL after options apply so host scoping
	// matches whatever URL we'll actually use for requests.
	finalURL, err := parseAndValidateBaseURL(opts2.baseURL)
	if err != nil {
		return nil, err
	}
	applyBearer(&opts2, finalURL.Host)
	return &Custom{
		opts: opts2,
		host: finalURL.Host,
	}, nil
}

// parseAndValidateBaseURL returns the parsed URL if it includes scheme
// and host, or a descriptive error otherwise.
func parseAndValidateBaseURL(baseURL string) (*url.URL, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("custom registry: parsing baseURL %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("custom registry: baseURL %q must include scheme and host", baseURL)
	}
	return u, nil
}

// BaseURL returns the base URL of this custom registry client.
func (r *Custom) BaseURL() string { return r.opts.baseURL }

// Host returns the network host of the registry (e.g. "registry.example.com"
// or "registry.internal:8443"). This is used by the Server to derive a
// stable on-disk cache directory and to scope bearer-token injection
// when no explicit WithBearerHost was supplied.
func (r *Custom) Host() string { return r.host }

// EndpointKey returns a stable, filesystem-safe identifier for this
// registry endpoint that distinguishes distinct paths on the same
// host. Two Custom registries configured at
// https://h.example.com/teamA/v1/modules and .../teamB/v1/modules
// share a Host but produce different EndpointKey values, so their
// on-disk caches do not collide. When the baseURL path is empty
// (discovery resolved to the host root) EndpointKey equals Host.
func (r *Custom) EndpointKey() string {
	return endpointKey(r.host, r.opts.baseURL)
}

// endpointKey composes a host with a short hash of the URL path when
// the path is non-empty. Shared by Custom and the service-discovery
// LazyCustom wrapper so cache layout is identical regardless of which
// entry point created the registry.
func endpointKey(host, fullURL string) string {
	if host == "" {
		return ""
	}
	u, err := url.Parse(fullURL)
	if err != nil {
		return host
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return host
	}
	sum := sha256.Sum256([]byte(fullURL))
	return host + "_" + hex.EncodeToString(sum[:4])
}

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
