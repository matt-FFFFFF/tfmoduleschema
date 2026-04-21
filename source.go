package tfmoduleschema

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// forcedGetterRE matches the "xxx::" forced-getter prefix syntax
// understood by go-getter (e.g. "git::", "s3::", "file::"). It is a
// re-implementation of the unexported forcedRegexp in go-getter so we
// can classify sources without depending on its private API.
var forcedGetterRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)::(.+)$`)

// urlSchemeRE matches a URL scheme prefix (e.g. "https://", "s3://").
var urlSchemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+\-.]*://`)

// windowsDriveRE matches a Windows drive-letter absolute path prefix
// (e.g. "C:\", "c:/"). Detection is platform-independent so Windows
// paths are classified correctly regardless of the host OS (relevant
// for tests and cross-platform source strings).
var windowsDriveRE = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// isLocalSource reports whether src refers to a path on the local
// filesystem. Local sources are inspected in place and never copied
// into the module cache.
//
// Classification:
//
//   - empty string: not local
//   - "file::..." or "file://...": local
//   - any other "<getter>::" prefix (git::, s3::, ...): remote
//   - any "<scheme>://" prefix: remote
//   - Unix absolute path ("/..."): local
//   - Windows drive-letter absolute path ("C:\..." / "c:/..."): local
//   - UNC path ("\\server\share\..."): local
//   - any path beginning with "." (e.g. "./foo", "../foo", ".",
//     "..", ".terraform/modules/x"): local. Terraform and OpenTofu
//     registry addresses never start with a dot, so this is an
//     unambiguous local-path marker and is more forgiving than
//     Terraform's strict "./" / "../" rule for paths like
//     ".terraform/modules/foo" that users commonly pass in.
//   - anything else (bare shorthand like "github.com/x/y",
//     "mydir/mymodule"): remote, matching Terraform's module-source
//     conventions where undecorated paths are registry addresses.
//
// The actual path normalisation for local sources is performed by
// localSourcePath, which strips any "file::" / "file://" prefix (with
// proper URL decoding for the latter) and resolves the result with
// filepath.Abs.
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
	if windowsDriveRE.MatchString(src) {
		return true
	}
	if strings.HasPrefix(src, `\\`) {
		return true
	}
	// Any path beginning with "." is a local path. This covers
	// "./foo", "../foo", ".", "..", ".\foo", "..\foo", and also
	// ".terraform/modules/foo" — the latter being a common source
	// users pass after `terraform init` that Terraform itself would
	// not accept as a bare module source, but that go-getter's
	// relative-path handling cannot resolve without an explicit
	// "./" prefix.
	if strings.HasPrefix(src, ".") {
		return true
	}
	return false
}

// localSourcePath converts a local source string into an absolute
// filesystem path. Local paths are resolved directly against the
// current working directory with filepath.Abs; go-getter is not
// involved because local sources are inspected in place and the
// extra translation go-getter's FileDetector performs (such as
// forward-slashing paths on Windows for URL use) is inappropriate
// for paths we then hand to os.Stat / hclparse.
//
// Supported input forms:
//
//   - "file::<path>"  — forced-getter prefix; the remainder is a
//     plain filesystem path.
//   - "file://<path>" — RFC 8089 file URI. Parsed with net/url so
//     that percent-encoding, an optional "localhost" host, and
//     Windows drive-letter URIs ("file:///C:/foo") are handled
//     correctly.
//   - anything else  — taken as-is.
func localSourcePath(src string) (string, error) {
	s := src

	// Forced-getter "file::" prefix: treat the remainder as a plain
	// filesystem path, no URL decoding.
	if m := forcedGetterRE.FindStringSubmatch(s); m != nil && strings.EqualFold(m[1], "file") {
		s = m[2]
	} else if strings.HasPrefix(strings.ToLower(s), "file://") {
		// file:// URI: parse with net/url so we handle
		// percent-encoding, an optional "localhost" host, and
		// Windows drive-letter URIs ("file:///C:/foo") correctly.
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("parsing file URI %q: %w", src, err)
		}
		// Only "" and "localhost" are valid hosts in a file URI;
		// anything else (e.g. a UNC-style "file://server/share")
		// cannot be meaningfully resolved as a local path here.
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			return "", fmt.Errorf("file URI %q has non-local host %q", src, u.Host)
		}
		p := u.Path
		// On Windows, file URIs for drive paths look like
		// "file:///C:/foo" → Path "/C:/foo"; strip the leading
		// slash so filepath.Abs sees a real drive-letter path.
		if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		s = filepath.FromSlash(p)
	}

	if s == "" {
		return "", fmt.Errorf("empty local source path")
	}

	abs, err := filepath.Abs(s)
	if err != nil {
		return "", fmt.Errorf("resolving local source %q: %w", src, err)
	}
	return abs, nil
}
