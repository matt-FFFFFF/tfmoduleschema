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
// ".partial"; on success any existing dest is moved aside to
// "<dest>.backup", the partial is renamed into place, and the backup is
// removed. On rename failure the backup is restored so a transient
// fetch error cannot destroy the last known-good cache entry.
//
// Before starting a new download Fetch also performs recovery: if dest
// is missing but a "<dest>.backup" exists (from a previous call that
// crashed after the rename-to-backup step) the backup is restored to
// dest rather than blindly discarded.
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

	// Recover from an interrupted previous swap if possible. Treat
	// "<dest>.backup" as stale (safe to delete) only when dest is
	// present and healthy; if dest is missing but a backup exists,
	// restore it so we don't lose the last known-good cache entry.
	backup := dest + ".backup"
	switch _, err := os.Lstat(dest); {
	case err == nil:
		if rmErr := os.RemoveAll(backup); rmErr != nil {
			return fmt.Errorf("removing stale backup %q: %w", backup, rmErr)
		}
	case os.IsNotExist(err):
		if _, bErr := os.Lstat(backup); bErr == nil {
			if rnErr := os.Rename(backup, dest); rnErr != nil {
				return fmt.Errorf("restoring backup cache entry %q to %q: %w", backup, dest, rnErr)
			}
		} else if !os.IsNotExist(bErr) {
			return fmt.Errorf("inspecting backup cache entry %q: %w", backup, bErr)
		}
	default:
		return fmt.Errorf("inspecting existing cache entry %q: %w", dest, err)
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
	} else if !os.IsNotExist(err) {
		// Lstat failed for a reason other than "not there" (e.g.
		// permissions, IO). Bail rather than silently skipping the
		// backup and risking a confusing rename error below.
		_ = os.RemoveAll(partial)
		return fmt.Errorf("inspecting existing cache entry %q: %w", dest, err)
	}

	if err := os.Rename(partial, dest); err != nil {
		_ = os.RemoveAll(partial)
		if haveBackup {
			// Attempt rollback: restore the previous dest. If that
			// also fails the on-disk state is that dest is missing
			// and backup is present; surface both errors so callers
			// can diagnose without having to inspect the cache dir.
			if rbErr := os.Rename(backup, dest); rbErr != nil {
				return fmt.Errorf(
					"finalising cache entry %q: %w; restoring backup %q to %q also failed: %v",
					dest, err, backup, dest, rbErr,
				)
			}
		}
		return fmt.Errorf("finalising cache entry %q: %w", dest, err)
	}

	// The new cache entry is already in place, so backup cleanup is
	// best-effort only — any lingering backup will be removed by the
	// stale-backup handling at the top of the next successful call.
	// This avoids returning an error that would make callers (e.g.
	// Server.fetchModule) treat a fully-populated cache as a failure.
	if haveBackup {
		_ = os.RemoveAll(backup)
	}
	return nil
}
