package tfmoduleschema

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// EnvCacheDir is the environment variable used to override the module
// cache directory. When set (and non-empty), its value is used as the
// root of the module cache instead of the default.
const EnvCacheDir = "TFMODULESCHEMA_CACHE_DIR"

// CacheStatus indicates whether a module was served from the local cache
// or had to be downloaded from the registry.
type CacheStatus int

const (
	// CacheStatusMiss indicates the module was not in the cache and was
	// fetched from the registry.
	CacheStatusMiss CacheStatus = iota
	// CacheStatusHit indicates the module was served from the local
	// cache.
	CacheStatusHit
)

// String returns a human-readable form of the CacheStatus.
func (c CacheStatus) String() string {
	switch c {
	case CacheStatusHit:
		return "hit"
	case CacheStatusMiss:
		return "miss"
	default:
		return "unknown"
	}
}

// CacheStatusFunc is invoked by the Server after resolving a module
// request to report whether the module was found in the local cache
// (CacheStatusHit) or a download was attempted (CacheStatusMiss).
//
// For classic registry requests the request passed in has a concrete
// (fixed) version. For Source-mode requests (request.Source != "")
// the Version field holds whatever the caller supplied — which may
// be empty or a raw ref embedded in the go-getter URL — because
// there is no registry to resolve a constraint against. Callbacks
// that need a concrete version should branch on request.Source.
type CacheStatusFunc func(request Request, status CacheStatus)

// ServerOption configures a Server at construction time.
type ServerOption func(*Server)

// WithCacheDir overrides the module cache directory used by the Server.
// An empty dir is ignored and the default is used instead.
func WithCacheDir(dir string) ServerOption {
	return func(s *Server) {
		if dir != "" {
			s.cacheDir = dir
		}
	}
}

// WithForceFetch configures the Server to always re-download modules,
// bypassing any existing cache entries. Downloads still populate the
// cache.
func WithForceFetch(force bool) ServerOption {
	return func(s *Server) {
		s.forceFetch = force
	}
}

// WithHTTPClient overrides the *http.Client used for registry requests.
// A nil client is ignored.
func WithHTTPClient(c *http.Client) ServerOption {
	return func(s *Server) {
		if c != nil {
			s.httpClient = c
		}
	}
}

// WithCacheStatusFunc installs a callback invoked after the Server
// resolves a module to indicate whether the cache was hit or the module
// was downloaded.
func WithCacheStatusFunc(fn CacheStatusFunc) ServerOption {
	return func(s *Server) {
		s.cacheStatusFn = fn
	}
}

// defaultCacheDir returns the default module cache directory, honouring
// TFMODULESCHEMA_CACHE_DIR if set. Falls back to
// os.UserCacheDir()/tfmoduleschema, then os.TempDir()/tfmoduleschema-cache.
func defaultCacheDir() string {
	if v := os.Getenv(EnvCacheDir); v != "" {
		return v
	}
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return filepath.Join(d, "tfmoduleschema")
	}
	return filepath.Join(os.TempDir(), "tfmoduleschema-cache")
}

// cachePathSegment returns a filesystem-safe cache path segment. Empty
// values and traversal-only values are mapped to "default".
func cachePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." {
		return "default"
	}
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(value)
}

// normalizedRegistryType maps empty/unknown RegistryTypes to
// RegistryTypeOpenTofu so the cache layout is stable.
func normalizedRegistryType(r RegistryType) RegistryType {
	switch r {
	case RegistryTypeTerraform, RegistryTypeCustom:
		return r
	}
	return RegistryTypeOpenTofu
}

// sanitizeHost returns a filesystem-safe cache segment for a registry
// host. Hosts are lowercased (DNS is case-insensitive) so variations
// like "Host" and "host" share the same cache entry, then passed
// through cachePathSegment to strip any characters unsafe on disk
// (notably the ':' in host:port, which becomes '_').
func sanitizeHost(host string) string {
	return cachePathSegment(strings.ToLower(host))
}

// cacheModuleDir returns the cache directory for a given module request.
// The request version must be a concrete version.
//
// For the two public registries the layout is:
//
//	<cacheDir>/<registry>/<namespace>/<name>/<system>/<version>/
//
// For custom registries the host is included so distinct custom
// registries cannot collide:
//
//	<cacheDir>/custom/<host>/<namespace>/<name>/<system>/<version>/
//
// The host passed in should be the original input host (from
// --registry-url or WithCustomRegistry), not the service-discovery
// resolved host, so that edits to the configured registry reliably
// invalidate the cache.
func cacheModuleDir(cacheDir string, req Request, host string) string {
	reg := normalizedRegistryType(req.RegistryType)
	base := filepath.Join(cacheDir, cachePathSegment(string(reg)))
	if reg == RegistryTypeCustom {
		if host == "" {
			host = "default"
		}
		base = filepath.Join(base, sanitizeHost(host))
	}
	return filepath.Join(
		base,
		cachePathSegment(req.Namespace),
		cachePathSegment(req.Name),
		cachePathSegment(req.System),
		cachePathSegment(req.Version),
	)
}

// cacheDirExistsNonEmpty reports whether dir exists and contains at least
// one entry.
func cacheDirExistsNonEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// cacheSourceDir returns the cache directory for a raw go-getter source
// URL. The source string is hashed so any URL shape can be stored
// deterministically:
//
//	<cacheDir>/source/<hex-sha256(source)[:16]>/
//
// The suffix is the first 16 hex characters (8 bytes / 64 bits) of the
// SHA-256 digest of the source string — enough to avoid collisions in
// practice without producing unwieldy path segments.
func cacheSourceDir(cacheDir, source string) string {
	sum := sha256.Sum256([]byte(source))
	return filepath.Join(cacheDir, "source", hex.EncodeToString(sum[:])[:16])
}
