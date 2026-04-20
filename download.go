package tfmoduleschema

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	getter "github.com/hashicorp/go-getter/v2"
)

// downloader wraps go-getter to download a module source into a cache
// directory, with an atomic-rename "<dest>.partial" staging pattern so we
// never leave a half-extracted cache entry behind on failure.
type downloader struct{}

// Fetch downloads the module at src into dest (which must not already
// exist). The download is first extracted into dest + ".partial" and
// renamed atomically on success.
//
// go-getter interprets src URL prefixes such as "git::", "s3::", etc. and
// picks an appropriate getter. A "//subdir" suffix selects a subdirectory
// within the fetched archive/repo.
func (d *downloader) Fetch(ctx context.Context, src, dest string) error {
	if src == "" {
		return fmt.Errorf("empty download source URL")
	}

	// If the cache entry already exists as a directory, treat as success.
	// Callers should not call Fetch when they already see a hit, but this
	// guards against races.
	if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
		return nil
	}

	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("creating cache parent dir %q: %w", parent, err)
	}

	// Clean up any stale partial from a previous failed attempt.
	partial := dest + ".partial"
	if err := os.RemoveAll(partial); err != nil {
		return fmt.Errorf("removing stale partial %q: %w", partial, err)
	}

	client := &getter.Client{}
	req := &getter.Request{
		Src:     src,
		Dst:     partial,
		GetMode: getter.ModeAny,
	}
	if _, err := client.Get(ctx, req); err != nil {
		// On failure, wipe the partial so the next attempt starts clean.
		_ = os.RemoveAll(partial)
		return fmt.Errorf("downloading %s: %w", src, err)
	}

	if err := os.Rename(partial, dest); err != nil {
		_ = os.RemoveAll(partial)
		return fmt.Errorf("finalising cache entry %q: %w", dest, err)
	}
	return nil
}
