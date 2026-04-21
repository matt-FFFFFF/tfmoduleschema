package registry

import (
	"net/http"
	"strings"
)

// WithBearerToken attaches an Authorization: Bearer <token> header to
// every outbound registry request whose URL host matches the registry
// host. The header is never attached to cross-host redirects: if the
// server 30x-redirects the request to a different host (for example,
// pointing /download at a pre-signed S3 URL on a different domain),
// the token is stripped before the follow-up request is sent.
//
// An empty token is a no-op (the option is applied but the transport
// injects nothing), so callers can thread "maybe-empty" tokens through
// without branching.
//
// WithBearerToken composes with WithHTTPClient: if a caller supplies
// both, the bearer transport wraps the supplied client's transport.
// The host scope is taken from the registry's own base URL at apply
// time by default, so install this option AFTER WithBaseURL (or on a
// client constructed directly from a URL) if you override the
// default. To scope the token to a DIFFERENT host than the request
// URL's host (e.g. when service discovery yielded a modules.v1 URL
// on a different host than the user-configured input), use
// WithBearerHost.
func WithBearerToken(token string) Option {
	return func(o *options) {
		o.bearerToken = token
	}
}

// WithBearerHost overrides the host against which bearer-token
// injection is scoped. When set, the Authorization header is attached
// only to requests whose URL host matches this value (case-insensitive),
// regardless of the registry's own base URL.
//
// This is the correct hook when a token was resolved for one host
// (the user-facing input host) but registry requests actually go to
// a different host produced by service discovery. Scoping to the
// input host prevents leaking credentials to an untrusted discovered
// host.
//
// An empty host is treated as "unset" and the bearer transport falls
// back to the registry's own base URL host, matching the historical
// behaviour.
func WithBearerHost(host string) Option {
	return func(o *options) {
		o.bearerHost = host
	}
}

// applyBearer installs a host-scoped bearer-token RoundTripper on
// o.httpClient if a token was configured. It is called by registry
// constructors (OpenTofu, Terraform, Custom) after applyOptions has
// run so the final base URL is known. The caller passes the
// fall-back host (the registry's own base URL host); if
// WithBearerHost was supplied, that overrides the fall-back.
func applyBearer(o *options, fallbackHost string) {
	if o.bearerToken == "" {
		return
	}
	host := fallbackHost
	if o.bearerHost != "" {
		host = o.bearerHost
	}
	base := o.httpClient
	if base == nil {
		base = http.DefaultClient
	}
	// Clone the client so we don't mutate a caller-supplied one.
	clone := *base
	clone.Transport = &bearerTransport{
		base:  clone.Transport,
		host:  strings.ToLower(host),
		token: o.bearerToken,
	}
	// Strip the Authorization header on any cross-host redirect.
	prev := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if prev != nil {
			if err := prev(req, via); err != nil {
				return err
			}
		}
		// On every redirect, let the transport decide afresh whether
		// to inject the token. Remove any header the previous hop set
		// so it cannot leak cross-host.
		req.Header.Del("Authorization")
		return nil
	}
	o.httpClient = &clone
}

// bearerTransport injects Authorization: Bearer <token> on requests
// whose URL host matches `host`. Other hosts pass through unchanged.
type bearerTransport struct {
	base  http.RoundTripper
	host  string
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.shouldAuth(req) {
		// Clone so we don't mutate a shared request.
		r := req.Clone(req.Context())
		r.Header.Set("Authorization", "Bearer "+t.token)
		req = r
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func (t *bearerTransport) shouldAuth(req *http.Request) bool {
	if t.host == "" || t.token == "" {
		return false
	}
	return strings.EqualFold(req.URL.Host, t.host)
}
