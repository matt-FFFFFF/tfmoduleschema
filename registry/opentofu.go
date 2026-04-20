package registry

import (
	"context"

	goversion "github.com/hashicorp/go-version"
)

// OpenTofu is the Registry implementation for the OpenTofu public
// registry at registry.opentofu.org. OpenTofu returns download locations
// in a JSON body with a "location" field.
type OpenTofu struct {
	opts options
}

// NewOpenTofu constructs an OpenTofu registry client.
func NewOpenTofu(opts ...Option) *OpenTofu {
	return &OpenTofu{opts: applyOptions(DefaultOpenTofuBaseURL, opts)}
}

// BaseURL returns the base URL of this OpenTofu client.
func (r *OpenTofu) BaseURL() string { return r.opts.baseURL }

// ListVersions returns all versions of the requested module.
func (r *OpenTofu) ListVersions(ctx context.Context, req VersionsRequest) (goversion.Collection, error) {
	return listVersions(ctx, r.opts.httpClient, r.opts.baseURL, req)
}

// ResolveDownload returns the go-getter-compatible source URL for the
// given concrete version.
func (r *OpenTofu) ResolveDownload(ctx context.Context, req DownloadRequest) (string, error) {
	// OpenTofu prefers the JSON body.
	return resolveDownload(ctx, r.opts.httpClient, r.opts.baseURL, req, false)
}
