package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Errors returned by service discovery.
var (
	// ErrDiscoveryUnsupported indicates the host responded but does not
	// advertise the modules.v1 service — the host is reachable but not
	// a module registry.
	ErrDiscoveryUnsupported = errors.New("host does not advertise modules.v1")
	// ErrDiscoveryFailed wraps any other failure to perform service
	// discovery (network error, non-200, invalid JSON, etc.).
	ErrDiscoveryFailed = errors.New("service discovery failed")
)

// ModulesV1Key is the service-discovery key that identifies the module
// registry protocol version 1, per
// https://developer.hashicorp.com/terraform/internals/remote-service-discovery.
const ModulesV1Key = "modules.v1"

// DiscoverModulesEndpoint performs Terraform remote service discovery
// against the given input. input may be any of:
//
//   - a bare hostname ("registry.example.com")
//   - a hostname with a port ("registry.internal:8443")
//   - a full URL containing only a host ("https://registry.example.com")
//   - a full URL with a path, in which case discovery is skipped and
//     the URL is returned verbatim (trailing slash trimmed)
//
// On success it returns the absolute base URL of the modules.v1
// service, suitable for passing to NewCustom. The inputHost return is
// the original host supplied by the caller (lowercased, with port
// preserved), useful for credential lookup.
//
// HTTPS is required per the spec for discovery requests. An http://
// input with only a host is accepted and treated like host-only
// input: the scheme is discarded and HTTPS is used for discovery. A
// full URL with a path bypasses discovery entirely and is returned
// verbatim, in which case http:// is also allowed (the caller is
// asserting the modules.v1 endpoint directly, e.g. a test server).
func DiscoverModulesEndpoint(ctx context.Context, client *http.Client, input string) (baseURL, inputHost string, err error) {
	if client == nil {
		client = http.DefaultClient
	}

	bypass, host, perr := parseInputHost(input)
	if perr != nil {
		return "", "", perr
	}
	if bypass != "" {
		// Full URL with path → discovery bypassed.
		return bypass, host, nil
	}

	discoveryURL := "https://" + host + "/.well-known/terraform.json"
	base, err := fetchDiscovery(ctx, client, discoveryURL)
	if err != nil {
		return "", host, err
	}
	return base, host, nil
}

// parseInputHost classifies a custom-registry input string without
// performing any network I/O.
//
// It returns:
//
//   - bypass: non-empty when input is already a full URL with a path,
//     in which case the caller should skip service discovery and use
//     this value as the registry base URL.
//   - host: the lowercased host (with port preserved) to use for
//     credential lookup and cache keying. Always populated on success.
//   - err: non-nil when the input is obviously malformed.
func parseInputHost(input string) (bypass, host string, err error) {
	if strings.TrimSpace(input) == "" {
		return "", "", fmt.Errorf("%w: empty input", ErrDiscoveryFailed)
	}
	// Full URL? (has scheme + host)
	if u, perr := url.Parse(input); perr == nil && u.Scheme != "" && u.Host != "" {
		scheme := strings.ToLower(u.Scheme)
		if scheme != "https" && scheme != "http" {
			return "", "", fmt.Errorf("%w: unsupported URL scheme %q (only https/http are accepted)", ErrDiscoveryFailed, u.Scheme)
		}
		if p := strings.Trim(u.Path, "/"); p != "" {
			// Full URL with path: bypass discovery. http is allowed
			// here only because the caller is asserting they know
			// the modules.v1 endpoint outright (e.g. a test server).
			return strings.TrimRight(input, "/"), strings.ToLower(u.Host), nil
		}
		// scheme + host + no path → treat the Host as the discovery
		// input. Discovery itself always uses https per the spec;
		// the scheme of the input URL is discarded.
		input = u.Host
	}
	h := strings.ToLower(strings.TrimSpace(input))
	if h == "" {
		return "", "", fmt.Errorf("%w: empty host", ErrDiscoveryFailed)
	}
	if strings.ContainsAny(h, "/? ") {
		return "", "", fmt.Errorf("%w: %q is not a host", ErrDiscoveryFailed, h)
	}
	return "", h, nil
}

// DiscoverModulesEndpointInputCheck validates that input is a
// syntactically valid custom-registry input without performing any
// network I/O. It is intended for use by code that wants to fail fast
// on malformed configuration.
func DiscoverModulesEndpointInputCheck(input string) (bypass, host string, err error) {
	return parseInputHost(input)
}

// discoveryDoc is the shape of the JSON document returned by
// .well-known/terraform.json. Only modules.v1 is read; other keys are
// ignored. Values are kept as raw JSON because some services (e.g.
// login.v1 on TFC/HCP) advertise objects rather than plain URL strings,
// and the zero-or-more extra keys must not cause the whole parse to
// fail.
type discoveryDoc map[string]json.RawMessage

func fetchDiscovery(ctx context.Context, client *http.Client, discoveryURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: building request: %v", ErrDiscoveryFailed, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: GET %s: %v", ErrDiscoveryFailed, discoveryURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: GET %s: %s", ErrDiscoveryFailed, discoveryURL, resp.Status)
	}

	ct := resp.Header.Get("Content-Type")
	// Accept application/json and application/json; charset=utf-8 etc.
	if mt := strings.SplitN(ct, ";", 2)[0]; strings.TrimSpace(strings.ToLower(mt)) != "application/json" {
		return "", fmt.Errorf("%w: GET %s: unexpected content-type %q", ErrDiscoveryFailed, discoveryURL, ct)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("%w: reading body: %v", ErrDiscoveryFailed, err)
	}

	var doc discoveryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("%w: parsing JSON: %v", ErrDiscoveryFailed, err)
	}

	raw, ok := doc[ModulesV1Key]
	if !ok || len(raw) == 0 {
		return "", fmt.Errorf("%w: %s not present in discovery document", ErrDiscoveryUnsupported, ModulesV1Key)
	}
	var ref string
	if err := json.Unmarshal(raw, &ref); err != nil {
		return "", fmt.Errorf("%w: %s must be a URL string, got %s", ErrDiscoveryFailed, ModulesV1Key, string(raw))
	}
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("%w: %s is empty in discovery document", ErrDiscoveryUnsupported, ModulesV1Key)
	}

	// Resolve against the FINAL discovery URL (after redirects).
	// resp.Request.URL reflects the last URL in the redirect chain.
	finalURL := resp.Request.URL
	base, err := resolveDiscoveryReference(finalURL, ref)
	if err != nil {
		return "", fmt.Errorf("%w: resolving modules.v1 value %q: %v", ErrDiscoveryFailed, ref, err)
	}
	return base, nil
}

// resolveDiscoveryReference resolves a modules.v1 reference against
// the final discovery URL. Both absolute and relative forms are
// supported per the spec. Any trailing slash is stripped so the result
// can be joined with "/<ns>/<name>/<sys>/..." without accidentally
// producing a double slash.
func resolveDiscoveryReference(base *url.URL, ref string) (string, error) {
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(refURL)
	return strings.TrimRight(resolved.String(), "/"), nil
}
