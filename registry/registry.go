// Package registry defines the Registry interface used by
// tfmoduleschema to talk to OpenTofu or HashiCorp Terraform module
// registries, along with the two public implementations.
package registry

import (
	"context"
	"errors"
	"net/http"

	goversion "github.com/hashicorp/go-version"
)

// Default base URLs for the two public module registries.
const (
	DefaultOpenTofuBaseURL  = "https://registry.opentofu.org/v1/modules"
	DefaultTerraformBaseURL = "https://registry.terraform.io/v1/modules"
)

// Errors returned by Registry implementations.
var (
	// ErrModuleNotFound is returned when the registry reports that no
	// module exists at the given address.
	ErrModuleNotFound = errors.New("module not found")
	// ErrRegistryAPI wraps non-2xx responses that are not 404s.
	ErrRegistryAPI = errors.New("registry API error")
)

// VersionsRequest identifies a module for the purposes of listing versions.
type VersionsRequest struct {
	Namespace string
	Name      string
	System    string
}

// DownloadRequest identifies a specific module version to resolve for
// download.
type DownloadRequest struct {
	Namespace string
	Name      string
	System    string
	Version   string // must be a concrete version
}

// Registry abstracts a Terraform-style module registry. It exposes the two
// endpoints of the minimal module registry protocol that tfmoduleschema
// needs: list versions, and resolve a download URL for a concrete version.
type Registry interface {
	// BaseURL returns the HTTP base URL of this registry
	// (e.g. "https://registry.opentofu.org/v1/modules"), without a
	// trailing slash.
	BaseURL() string

	// ListVersions returns all versions advertised by the registry for
	// the given module, sorted in the order the registry returned them.
	// It returns ErrModuleNotFound for a 404 response.
	ListVersions(ctx context.Context, req VersionsRequest) (goversion.Collection, error)

	// ResolveDownload returns a go-getter-compatible source URL from
	// which the given module version can be downloaded. It returns
	// ErrModuleNotFound for a 404 response.
	ResolveDownload(ctx context.Context, req DownloadRequest) (string, error)
}

// Option customises a Registry implementation at construction time.
type Option func(*options)

type options struct {
	baseURL    string
	httpClient *http.Client
}

// WithBaseURL overrides the default base URL. Useful in tests and to point
// at mirrors.
func WithBaseURL(u string) Option { return func(o *options) { o.baseURL = u } }

// WithHTTPClient overrides the default *http.Client used for registry
// requests.
func WithHTTPClient(c *http.Client) Option { return func(o *options) { o.httpClient = c } }

func applyOptions(defaultBase string, opts []Option) options {
	o := options{baseURL: defaultBase, httpClient: http.DefaultClient}
	for _, opt := range opts {
		opt(&o)
	}
	if o.httpClient == nil {
		o.httpClient = http.DefaultClient
	}
	return o
}
