package tfmoduleschema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEphemeralVariables_HCL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
variable "plain" {
  type = string
}
variable "ghost" {
  type      = string
  ephemeral = true
}
variable "constant_expr" {
  type      = string
  ephemeral = 1 == 1
}
variable "explicit_false" {
  type      = string
  ephemeral = false
}
`)
	got := ephemeralVariables(dir)
	assert.Equal(t, map[string]bool{"ghost": true, "constant_expr": true}, got)
}

func TestEphemeralVariables_JSONObjectShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "vars.tf.json", `{
  "variable": {
    "token":   { "type": "string", "ephemeral": true, "sensitive": true },
    "name":    { "type": "string" },
    "stringy": { "type": "string", "ephemeral": "true" }
  }
}`)
	got := ephemeralVariables(dir)
	assert.Equal(t, map[string]bool{"token": true}, got)
}

func TestEphemeralVariables_JSONArrayShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "vars.tf.json", `{
  "variable": [
    { "alpha": { "type": "string", "ephemeral": true } },
    { "beta":  { "type": "string" } }
  ]
}`)
	got := ephemeralVariables(dir)
	assert.Equal(t, map[string]bool{"alpha": true}, got)
}

func TestEphemeralVariables_OverrideFilesSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The "real" file declares no ephemeral variables; overrides try to
	// flip them on. We must not read overrides.
	writeFile(t, dir, "main.tf", `variable "x" {
  type = string
}`)
	writeFile(t, dir, "override.tf", `variable "x" {
  ephemeral = true
}`)
	writeFile(t, dir, "extras_override.tf", `variable "y" {
  type      = string
  ephemeral = true
}`)
	writeFile(t, dir, "override.tf.json", `{"variable":{"x":{"ephemeral":true}}}`)
	writeFile(t, dir, "extras_override.tf.json", `{"variable":{"z":{"ephemeral":true}}}`)
	got := ephemeralVariables(dir)
	assert.Empty(t, got)
}

func TestEphemeralVariables_MixedHCLAndJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
variable "hcl_one" {
  ephemeral = true
}
`)
	writeFile(t, dir, "extra.tf.json", `{"variable":{"json_one":{"ephemeral":true}}}`)
	got := ephemeralVariables(dir)
	assert.Equal(t, map[string]bool{"hcl_one": true, "json_one": true}, got)
}

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644))
}
