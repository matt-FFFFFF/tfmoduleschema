package tfmoduleschema

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDownloader_Fetch_LocalFile exercises go-getter's local-dir getter
// by pointing at a directory on disk. This avoids network use.
func TestDownloader_Fetch_LocalFile(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.tf"), []byte(`variable "x" {}`), 0o644))

	dest := filepath.Join(t.TempDir(), "out")
	d := &downloader{}
	require.NoError(t, d.Fetch(context.Background(), src, dest))

	// Destination should exist with main.tf copied in.
	_, err := os.Stat(filepath.Join(dest, "main.tf"))
	assert.NoError(t, err)
	// Partial staging dir should not remain.
	_, err = os.Stat(dest + ".partial")
	assert.True(t, os.IsNotExist(err), "partial should be gone, got %v", err)
}

// TestDownloader_Fetch_HTTPArchive exercises the http getter via an
// httptest server serving a small tar.gz archive.
func TestDownloader_Fetch_HTTPArchive(t *testing.T) {
	t.Parallel()
	// Build a .tar.gz in memory containing main.tf
	tgz := buildTarGz(t, map[string]string{"main.tf": `output "y" { value = 1 }`})

	mux := http.NewServeMux()
	mux.HandleFunc("/archive.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(tgz)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "http-out")
	d := &downloader{}
	// Force archive detection via "archive=tar.gz" query param that
	// go-getter understands.
	src := srv.URL + "/archive.tar.gz?archive=tar.gz"
	require.NoError(t, d.Fetch(context.Background(), src, dest))

	_, err := os.Stat(filepath.Join(dest, "main.tf"))
	assert.NoError(t, err)
}

// TestDownloader_Fetch_ReplacesExistingDest: when dest already exists,
// Fetch must replace its contents with a fresh copy (it is not
// idempotent by itself — the Server's cache-hit check provides
// idempotency). This is the regression test for issue #6 at the
// downloader layer.
func TestDownloader_Fetch_ReplacesExistingDest(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.tf"), []byte(`variable "fresh" {}`), 0o644))

	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	// Pre-seed with a stale file that must be removed by Fetch.
	require.NoError(t, os.WriteFile(filepath.Join(dest, "stale.tf"), []byte("stale"), 0o644))

	d := &downloader{}
	require.NoError(t, d.Fetch(context.Background(), src, dest))

	// Fresh file should be present.
	_, err := os.Stat(filepath.Join(dest, "main.tf"))
	assert.NoError(t, err)
	// Stale file must be gone.
	_, err = os.Stat(filepath.Join(dest, "stale.tf"))
	assert.True(t, os.IsNotExist(err), "stale file should have been removed, got %v", err)
}
