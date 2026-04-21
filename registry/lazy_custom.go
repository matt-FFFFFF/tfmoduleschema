package registry

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	goversion "github.com/hashicorp/go-version"
)

// LazyCustom wraps a Custom registry whose base URL is resolved on
// first use via Terraform remote service discovery. It implements the
// Registry interface and is safe for concurrent use.
//
// The caller-supplied input (host, host:port, or full URL) is used for
// cache keying and credential lookup via Host(); the discovered
// modules.v1 endpoint is used for actual registry API calls.
type LazyCustom struct {
	input string
	opts  []Option

	httpClient *http.Client

	once sync.Once
	c    *Custom
	err  error
}

// NewLazyCustom returns a Registry that will perform service discovery
// against input on first use. input may be any form accepted by
// DiscoverModulesEndpoint: a bare host, host:port, a scheme+host URL
// (triggering discovery), or a full URL including a path (no
// discovery).
//
// httpClient, when non-nil, is used for the discovery request and for
// the underlying registry API calls. A nil httpClient falls back to
// http.DefaultClient.
func NewLazyCustom(input string, httpClient *http.Client, opts ...Option) *LazyCustom {
	return &LazyCustom{
		input:      input,
		opts:       opts,
		httpClient: httpClient,
	}
}

// Host returns the input host (lowercased, with port preserved) used
// for this custom registry. This is the value callers should use for
// cache keying and credential lookup, regardless of whether the
// discovered modules.v1 URL points at a different host. Host works
// without triggering discovery.
func (l *LazyCustom) Host() string {
	_, host, err := parseInputHost(l.input)
	if err != nil {
		return ""
	}
	return host
}

// BaseURL triggers resolution and returns the resolved base URL, or
// an empty string if resolution failed (the error is surfaced via the
// Registry methods instead).
func (l *LazyCustom) BaseURL() string {
	if err := l.resolve(context.Background()); err != nil {
		return ""
	}
	return l.c.BaseURL()
}

// ListVersions resolves the endpoint if not already done and proxies
// to the underlying Custom.
func (l *LazyCustom) ListVersions(ctx context.Context, req VersionsRequest) (goversion.Collection, error) {
	if err := l.resolve(ctx); err != nil {
		return nil, err
	}
	return l.c.ListVersions(ctx, req)
}

// ResolveDownload resolves the endpoint if not already done and
// proxies to the underlying Custom.
func (l *LazyCustom) ResolveDownload(ctx context.Context, req DownloadRequest) (string, error) {
	if err := l.resolve(ctx); err != nil {
		return "", err
	}
	return l.c.ResolveDownload(ctx, req)
}

func (l *LazyCustom) resolve(ctx context.Context) error {
	l.once.Do(func() {
		base, inputHost, err := DiscoverModulesEndpoint(ctx, l.httpClient, l.input)
		if err != nil {
			l.err = fmt.Errorf("custom registry discovery: %w", err)
			return
		}
		// Ensure the underlying Custom uses the same httpClient as
		// discovery (for auth transports installed by the caller).
		// Scope bearer injection to the INPUT host (the host a token
		// was resolved for via TF_TOKEN_<host> / credentials.tfrc.json)
		// so that a discovered modules.v1 endpoint on a different
		// host does not receive credentials it was not meant to see.
		// WithBearerHost is applied LAST so it wins over any earlier
		// (default or caller-supplied) setting; empty inputHost falls
		// back to the base URL host naturally inside applyBearer.
		opts := l.opts
		if l.httpClient != nil {
			opts = append(opts, WithHTTPClient(l.httpClient))
		}
		if inputHost != "" {
			opts = append(opts, WithBearerHost(inputHost))
		}
		c, err := NewCustom(base, opts...)
		if err != nil {
			l.err = fmt.Errorf("custom registry from discovered base %q: %w", base, err)
			return
		}
		l.c = c
	})
	return l.err
}
