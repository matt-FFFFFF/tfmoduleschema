package tfmoduleschema

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/matt-FFFFFF/tfmoduleschema/registry"
)

// ErrInvalidRequest is returned when a Request fails validation.
var ErrInvalidRequest = errors.New("invalid request")

// Server orchestrates module resolution, download, caching, and
// inspection.
type Server struct {
	l  *slog.Logger
	mu sync.RWMutex

	// configuration
	cacheDir      string
	forceFetch    bool
	httpClient    *http.Client
	cacheStatusFn CacheStatusFunc
	registries    map[RegistryType]registry.Registry

	// in-memory caches
	moduleCache map[moduleKey]*Module

	downloader *downloader
}

// moduleKey is the canonical cache key for an in-memory Module entry.
// Submodule path is included so root and submodule results are distinct.
// host is only populated for RegistryTypeCustom entries and keeps
// entries from distinct custom registries separate.
type moduleKey struct {
	registry RegistryType
	host     string
	ns, name string
	system   string
	version  string
	subpath  string
}

// NewServer creates a Server with the provided logger (defaulting to a
// discard logger when nil) and options.
func NewServer(l *slog.Logger, opts ...ServerOption) *Server {
	if l == nil {
		l = slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	s := &Server{
		l:           l,
		cacheDir:    defaultCacheDir(),
		httpClient:  http.DefaultClient,
		moduleCache: map[moduleKey]*Module{},
		registries:  map[RegistryType]registry.Registry{},
		downloader:  &downloader{},
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.httpClient == nil {
		s.httpClient = http.DefaultClient
	}
	return s
}

// WithRegistry overrides the registry client used for a given
// RegistryType. Useful for tests and private registries.
func WithRegistry(t RegistryType, r registry.Registry) ServerOption {
	return func(s *Server) {
		if s.registries == nil {
			s.registries = map[RegistryType]registry.Registry{}
		}
		if r != nil {
			s.registries[t] = r
		}
	}
}

// WithCustomRegistry installs a custom (private, mirror, or
// self-hosted) module registry. input may be any of:
//
//   - a full URL with path, e.g. "https://registry.internal/v1/modules"
//   - a host-only URL, e.g. "https://compliance.tf"
//   - a bare host, e.g. "compliance.tf" or "registry.internal:8443"
//
// For host-only inputs, Terraform remote service discovery is used at
// first call to resolve the modules.v1 endpoint:
// https://developer.hashicorp.com/terraform/internals/remote-service-discovery
//
// The input host (not the discovered endpoint host) is used for cache
// keying and credential lookup, so switching --registry-url reliably
// invalidates the cache even if two hosts publish the same modules.v1
// base.
func WithCustomRegistry(input string, opts ...registry.Option) ServerOption {
	return func(s *Server) {
		// Validate the input shape up front so obvious programmer
		// errors surface at option-application time rather than at
		// first request.
		_, host, err := registry.DiscoverModulesEndpointInputCheck(input)
		if err != nil {
			panic(fmt.Errorf("WithCustomRegistry: %w", err))
		}
		if s.registries == nil {
			s.registries = map[RegistryType]registry.Registry{}
		}
		// Resolve a token from TF_TOKEN_<host> / credentials.tfrc.json
		// for the INPUT host. A caller-supplied WithBearerToken in
		// opts takes precedence because options apply in order and
		// both write to the same field — the explicit one, passed
		// last, overwrites the resolved default.
		//
		// A resolver error (e.g. malformed credentials file) is
		// intentionally non-fatal here: the custom registry may not
		// require auth at all, and failing option application would
		// prevent even unauthenticated calls. If a token really is
		// needed, the registry call itself will return 401.
		resolvedOpts := opts
		if tok, _ := registry.ResolveTokenForHost(host); tok != "" {
			resolvedOpts = append([]registry.Option{registry.WithBearerToken(tok)}, opts...)
		}
		s.registries[RegistryTypeCustom] = registry.NewLazyCustom(input, s.httpClient, resolvedOpts...)
	}
}

// CacheDir returns the cache directory in use by this Server.
func (s *Server) CacheDir() string { return s.cacheDir }

// Cleanup is a no-op today, reserved for future resource release
// (temporary directories, etc.). Always safe to call.
func (s *Server) Cleanup() error { return nil }

// registryFor returns the registry client for the given RegistryType,
// constructing a default for the public registries if none was injected
// via WithRegistry. RegistryTypeCustom has no default and must be
// installed via WithCustomRegistry or WithRegistry before use.
func (s *Server) registryFor(t RegistryType) (registry.Registry, error) {
	t = normalizedRegistryType(t)
	if r, ok := s.registries[t]; ok && r != nil {
		return r, nil
	}
	switch t {
	case RegistryTypeTerraform:
		return registry.NewTerraform(registry.WithHTTPClient(s.httpClient)), nil
	case RegistryTypeCustom:
		return nil, fmt.Errorf("%w: no custom registry configured; install one with WithCustomRegistry or WithRegistry(RegistryTypeCustom, ...)", ErrInvalidRequest)
	default:
		return registry.NewOpenTofu(registry.WithHTTPClient(s.httpClient)), nil
	}
}

// registryHostFor returns the host string used for cache path
// derivation for the given RegistryType. Only custom registries
// produce a non-empty host — public registries fold host into their
// fixed RegistryType identifier. It returns an error when a custom
// registry is requested but not configured.
func (s *Server) registryHostFor(t RegistryType) (string, error) {
	if normalizedRegistryType(t) != RegistryTypeCustom {
		return "", nil
	}
	r, err := s.registryFor(t)
	if err != nil {
		return "", err
	}
	type hoster interface{ Host() string }
	if h, ok := r.(hoster); ok {
		return h.Host(), nil
	}
	// An injected custom registry that doesn't expose Host() — fall
	// back to a stable placeholder so the cache path is still
	// deterministic.
	return "custom", nil
}

// normalise returns a copy of req with defaults applied and basic
// validation performed.
func (s *Server) normalise(req Request) (Request, error) {
	req.RegistryType = normalizedRegistryType(req.RegistryType)
	if err := validateSegment("namespace", req.Namespace); err != nil {
		return req, err
	}
	if err := validateSegment("name", req.Name); err != nil {
		return req, err
	}
	if err := validateSegment("system", req.System); err != nil {
		return req, err
	}
	return req, nil
}

func validateSegment(field, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s must not be empty", ErrInvalidRequest, field)
	}
	for _, r := range value {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == 0:
			return fmt.Errorf("%w: %s contains forbidden character %q", ErrInvalidRequest, field, r)
		}
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%w: %s must not be %q", ErrInvalidRequest, field, value)
	}
	return nil
}

// fetchModule ensures that the module source for req (with a concrete
// version) is available on disk, returning the cache directory. It
// reports cache status via cacheStatusFn.
func (s *Server) fetchModule(ctx context.Context, req Request) (string, error) {
	host, err := s.registryHostFor(req.RegistryType)
	if err != nil {
		return "", err
	}
	dir := cacheModuleDir(s.cacheDir, req, host)

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.forceFetch && cacheDirExistsNonEmpty(dir) {
		s.fireCacheStatus(req, CacheStatusHit)
		s.l.Debug("module cache hit", "request", req, "dir", dir)
		return dir, nil
	}

	// Miss. Ask the registry for the download URL.
	reg, err := s.registryFor(req.RegistryType)
	if err != nil {
		return "", err
	}
	loc, err := reg.ResolveDownload(ctx, registry.DownloadRequest{
		Namespace: req.Namespace, Name: req.Name, System: req.System, Version: req.Version,
	})
	if err != nil {
		return "", fmt.Errorf("resolving download: %w", err)
	}
	s.l.Debug("resolved download", "request", req, "location", loc)

	s.fireCacheStatus(req, CacheStatusMiss)
	if err := s.downloader.Fetch(ctx, loc, dir); err != nil {
		return "", fmt.Errorf("fetching module: %w", err)
	}
	return dir, nil
}

func (s *Server) fireCacheStatus(req Request, status CacheStatus) {
	if s.cacheStatusFn != nil {
		s.cacheStatusFn(req, status)
	}
}

// fetchSource resolves a raw go-getter Source URL to a directory on
// disk, either by returning the local path directly (for local
// sources) or by downloading into a hashed cache directory. The
// in-memory mutex is used to serialise concurrent downloads of the
// same source.
func (s *Server) fetchSource(ctx context.Context, req Request) (string, error) {
	if isLocalSource(req.Source) {
		abs, err := localSourcePath(req.Source)
		if err != nil {
			return "", err
		}
		if _, err := statDir(abs); err != nil {
			return "", fmt.Errorf("local source %q: %w", req.Source, err)
		}
		s.fireCacheStatus(req, CacheStatusHit)
		s.l.Debug("local source", "source", req.Source, "dir", abs)
		return abs, nil
	}

	dir := cacheSourceDir(s.cacheDir, req.Source)

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.forceFetch && cacheDirExistsNonEmpty(dir) {
		s.fireCacheStatus(req, CacheStatusHit)
		s.l.Debug("source cache hit", "source", req.Source, "dir", dir)
		return dir, nil
	}

	s.fireCacheStatus(req, CacheStatusMiss)
	s.l.Debug("downloading source", "source", req.Source, "dir", dir)
	if err := s.downloader.Fetch(ctx, req.Source, dir); err != nil {
		return "", fmt.Errorf("fetching source %q: %w", req.Source, err)
	}
	return dir, nil
}

// discardWriter is an io.Writer that drops all output; used for the
// fallback logger when none is provided.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
