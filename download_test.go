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

// TestDownloader_Fetch_Idempotent: a second call when dest already exists
// should be a no-op.
func TestDownloader_Fetch_Idempotent(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	// pre-seed
	require.NoError(t, os.WriteFile(filepath.Join(dest, "x"), []byte("y"), 0o644))

	d := &downloader{}
	require.NoError(t, d.Fetch(context.Background(), "/nonexistent/path/should/not/be/used", dest))
	// File should still be there.
	_, err := os.Stat(filepath.Join(dest, "x"))
	assert.NoError(t, err)
}
