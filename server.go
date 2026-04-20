package tfmoduleschema

import (
	"log/slog"
	"net/http"
	"sync"
)

// Server orchestrates module resolution, download, caching, and
// inspection. Its public methods are defined alongside domain-specific
// files (schema.go, versions.go).
type Server struct {
	l  *slog.Logger
	mu sync.RWMutex

	// configuration
	cacheDir      string
	forceFetch    bool
	httpClient    *http.Client
	cacheStatusFn CacheStatusFunc
	registries    map[RegistryType]any // populated by WithRegistry; resolved at call time

	// in-memory caches
	moduleCache map[moduleKey]*Module
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
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.httpClient == nil {
		s.httpClient = http.DefaultClient
	}
	return s
}

// CacheDir returns the cache directory in use by this Server.
func (s *Server) CacheDir() string { return s.cacheDir }

// discardWriter is an io.Writer that drops all output; used for the
// fallback logger when none is provided.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
