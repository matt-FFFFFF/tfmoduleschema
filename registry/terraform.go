package registry

import (
	"context"

	goversion "github.com/hashicorp/go-version"
)

// Terraform is the Registry implementation for the HashiCorp Terraform
// public registry at registry.terraform.io. HashiCorp returns download
// locations in the X-Terraform-Get header (usually with a 204 response).
type Terraform struct {
	opts options
}

// NewTerraform constructs a HashiCorp Terraform registry client.
func NewTerraform(opts ...Option) *Terraform {
	return &Terraform{opts: applyOptions(DefaultTerraformBaseURL, opts)}
}

// BaseURL returns the base URL of this Terraform client.
func (r *Terraform) BaseURL() string { return r.opts.baseURL }

// ListVersions returns all versions of the requested module.
func (r *Terraform) ListVersions(ctx context.Context, req VersionsRequest) (goversion.Collection, error) {
	return listVersions(ctx, r.opts.httpClient, r.opts.baseURL, req)
}

// ResolveDownload returns the go-getter-compatible source URL for the
// given concrete version.
func (r *Terraform) ResolveDownload(ctx context.Context, req DownloadRequest) (string, error) {
	// HashiCorp Terraform prefers the X-Terraform-Get header.
	return resolveDownload(ctx, r.opts.httpClient, r.opts.baseURL, req, true)
}
