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

// Fetch downloads the module at src into dest, replacing whatever is
// currently at dest. The download is first extracted into dest +
// ".partial"; on success any existing dest is moved aside to a backup,
// the partial is renamed into place, and the backup is removed. On
// failure the existing dest is restored so a transient fetch error
// cannot destroy the last known-good cache entry.
//
// go-getter interprets src URL prefixes such as "git::", "s3::", etc. and
// picks an appropriate getter. A "//subdir" suffix selects a subdirectory
// within the fetched archive/repo.
func (d *downloader) Fetch(ctx context.Context, src, dest string) error {
	if src == "" {
		return fmt.Errorf("empty download source URL")
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
	// And any stale backup from a previous failed attempt.
	backup := dest + ".backup"
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("removing stale backup %q: %w", backup, err)
	}

	client := &getter.Client{}
	req := &getter.Request{
		Src:     src,
		Dst:     partial,
		GetMode: getter.ModeAny,
	}
	if _, err := client.Get(ctx, req); err != nil {
		// On failure, wipe the partial so the next attempt starts clean.
		// Leave any existing dest untouched — the caller's last
		// known-good cache entry is preserved.
		_ = os.RemoveAll(partial)
		return fmt.Errorf("downloading %s: %w", src, err)
	}

	// Download succeeded. Swap the partial into place, preserving the
	// previous dest in a backup so we can roll back on rename failure.
	haveBackup := false
	if _, err := os.Lstat(dest); err == nil {
		if err := os.Rename(dest, backup); err != nil {
			_ = os.RemoveAll(partial)
			return fmt.Errorf("backing up existing cache entry %q: %w", dest, err)
		}
		haveBackup = true
	}

	if err := os.Rename(partial, dest); err != nil {
		_ = os.RemoveAll(partial)
		if haveBackup {
			// Best-effort rollback: restore the previous dest.
			_ = os.Rename(backup, dest)
		}
		return fmt.Errorf("finalising cache entry %q: %w", dest, err)
	}

	if haveBackup {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("removing backup cache entry %q: %w", backup, err)
		}
	}
	return nil
}
