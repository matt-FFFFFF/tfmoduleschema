package tfmoduleschema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheStatusString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hit", CacheStatusHit.String())
	assert.Equal(t, "miss", CacheStatusMiss.String())
	assert.Equal(t, "unknown", CacheStatus(99).String())
}

func TestCachePathSegment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"", "default"},
		{".", "default"},
		{"..", "default"},
		{"Azure", "Azure"},
		{"a/b", "a_b"},
		{`a\b`, "a_b"},
		{"a:b", "a_b"},
		{"  trim  ", "trim"},
	} {
		assert.Equal(t, tc.want, cachePathSegment(tc.in), tc.in)
	}
}

func TestNormalizedRegistryType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, RegistryTypeTerraform, normalizedRegistryType(RegistryTypeTerraform))
	assert.Equal(t, RegistryTypeOpenTofu, normalizedRegistryType(RegistryTypeOpenTofu))
	assert.Equal(t, RegistryTypeOpenTofu, normalizedRegistryType(""))
	assert.Equal(t, RegistryTypeOpenTofu, normalizedRegistryType("weird"))
}

func TestCacheModuleDir(t *testing.T) {
	t.Parallel()
	got := cacheModuleDir("/tmp/x", Request{
		Namespace:    "Azure",
		Name:         "avm-res-x",
		System:       "azurerm",
		Version:      "1.2.3",
		RegistryType: RegistryTypeTerraform,
	})
	want := filepath.Join("/tmp/x", "terraform", "Azure", "avm-res-x", "azurerm", "1.2.3")
	assert.Equal(t, want, got)

	// empty registry type defaults to opentofu
	got = cacheModuleDir("/tmp/x", Request{Namespace: "n", Name: "m", System: "s", Version: "0"})
	want = filepath.Join("/tmp/x", "opentofu", "n", "m", "s", "0")
	assert.Equal(t, want, got)
}

func TestDefaultCacheDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvCacheDir, dir)
	assert.Equal(t, dir, defaultCacheDir())
}

func TestCacheDirExistsNonEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.False(t, cacheDirExistsNonEmpty(filepath.Join(dir, "does-not-exist")))
	assert.False(t, cacheDirExistsNonEmpty(dir), "fresh tempdir should be empty")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644))
	assert.True(t, cacheDirExistsNonEmpty(dir))
}
