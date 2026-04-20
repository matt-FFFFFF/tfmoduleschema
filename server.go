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
type moduleKey struct {
	registry RegistryType
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

// CacheDir returns the cache directory in use by this Server.
func (s *Server) CacheDir() string { return s.cacheDir }

// Cleanup is a no-op today, reserved for future resource release
// (temporary directories, etc.). Always safe to call.
func (s *Server) Cleanup() error { return nil }

// registryFor returns the registry client for the given RegistryType,
// constructing a default if none was injected via WithRegistry.
func (s *Server) registryFor(t RegistryType) registry.Registry {
	t = normalizedRegistryType(t)
	if r, ok := s.registries[t]; ok && r != nil {
		return r
	}
	switch t {
	case RegistryTypeTerraform:
		return registry.NewTerraform(registry.WithHTTPClient(s.httpClient))
	default:
		return registry.NewOpenTofu(registry.WithHTTPClient(s.httpClient))
	}
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
	dir := cacheModuleDir(s.cacheDir, req)

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.forceFetch && cacheDirExistsNonEmpty(dir) {
		s.fireCacheStatus(req, CacheStatusHit)
		s.l.Debug("module cache hit", "request", req, "dir", dir)
		return dir, nil
	}

	// Miss. Ask the registry for the download URL.
	reg := s.registryFor(req.RegistryType)
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

// discardWriter is an io.Writer that drops all output; used for the
// fallback logger when none is provided.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
