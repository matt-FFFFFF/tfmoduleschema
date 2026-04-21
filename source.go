package tfmoduleschema

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	getter "github.com/hashicorp/go-getter/v2"
)

// forcedGetterRE matches the "xxx::" forced-getter prefix syntax
// understood by go-getter (e.g. "git::", "s3::", "file::"). It is a
// re-implementation of the unexported forcedRegexp in go-getter so we
// can classify sources without depending on its private API.
var forcedGetterRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)::(.+)$`)

// urlSchemeRE matches a URL scheme prefix (e.g. "https://", "s3://").
var urlSchemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+\-.]*://`)

// isLocalSource reports whether src refers to a path on the local
// filesystem. Local sources are inspected in place and never copied
// into the module cache.
//
// Only explicit local-path forms are treated as local. Bare strings
// without a scheme, forced-getter prefix, or leading ./ ../ / are
// treated as remote go-getter shorthand (e.g. "github.com/org/repo"),
// matching Terraform's module-source conventions.
//
// The classification is:
//
//   - empty string: not local
//   - "file::..." or "file://...": local
//   - any other "<getter>::" prefix (git::, s3::, ...): remote
//   - any "<scheme>://" prefix: remote
//   - absolute path ("/..."): local
//   - relative path starting with "./", "../", or equal to "." / "..": local
//   - anything else (bare shorthand like "github.com/x/y"): remote
//
// The actual path normalisation for local sources is delegated to
// go-getter's FileDetector via localSourcePath.
func isLocalSource(src string) bool {
	if src == "" {
		return false
	}
	if m := forcedGetterRE.FindStringSubmatch(src); m != nil {
		return strings.EqualFold(m[1], "file")
	}
	if urlSchemeRE.MatchString(src) {
		return strings.HasPrefix(strings.ToLower(src), "file://")
	}
	if strings.HasPrefix(src, "/") {
		return true
	}
	if src == "." || src == ".." ||
		strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") {
		return true
	}
	return false
}

// localSourcePath converts a local source string into an absolute
// filesystem path using go-getter's FileDetector, which handles
// relative-path resolution against the current working directory and
// symlink following consistently with the rest of go-getter.
func localSourcePath(src string) (string, error) {
	// Strip any "file::" or "file://" prefix so we hand the detector a
	// plain filesystem path.
	s := src
	if m := forcedGetterRE.FindStringSubmatch(s); m != nil && strings.EqualFold(m[1], "file") {
		s = m[2]
	}
	if strings.HasPrefix(strings.ToLower(s), "file://") {
		s = s[len("file://"):]
	}
	if s == "" {
		return "", fmt.Errorf("empty local source path")
	}

	pwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolving working directory: %w", err)
	}

	d := new(getter.FileDetector)
	out, ok, err := d.Detect(s, pwd)
	if err != nil {
		return "", fmt.Errorf("detecting local source %q: %w", src, err)
	}
	if !ok || out == "" {
		return "", fmt.Errorf("go-getter FileDetector did not recognise %q as a local path", src)
	}
	return out, nil
}
