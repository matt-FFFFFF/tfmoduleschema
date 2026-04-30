package tfmoduleschema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ephemeralVariables walks every Terraform configuration file in dir
// (non-recursive) and returns the set of variable names declared with
// `ephemeral = true`. Both HCL (`*.tf`) and JSON (`*.tf.json`) syntaxes are
// supported. Override files (`override.tf`, `*_override.tf`,
// `override.tf.json`, `*_override.tf.json`) are skipped — they are rarely
// used to flip `ephemeral` and supporting them would require full merge
// semantics.
//
// terraform-config-inspect does not surface the `ephemeral` attribute on
// standard `variable` blocks, so we parse it ourselves. Constant boolean
// expressions are honoured; dynamic or non-constant expressions are ignored
// (they are vanishingly rare in variable blocks).
func ephemeralVariables(dir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	parser := hclparse.NewParser()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(dir, name)
		switch {
		case isTerraformOverrideFile(name):
			continue
		case strings.HasSuffix(name, ".tf.json"):
			collectEphemeralFromJSON(path, out)
		case strings.HasSuffix(name, ".tf"):
			collectEphemeralFromHCL(parser, path, out)
		}
	}
	return out
}

// isTerraformOverrideFile reports whether name is a Terraform override file
// in either HCL or JSON syntax.
func isTerraformOverrideFile(name string) bool {
	switch name {
	case "override.tf", "override.tf.json":
		return true
	}
	return strings.HasSuffix(name, "_override.tf") || strings.HasSuffix(name, "_override.tf.json")
}

// collectEphemeralFromHCL parses an HCL `*.tf` file and adds any
// `variable` blocks declared with a constant-true `ephemeral` expression
// to out.
func collectEphemeralFromHCL(parser *hclparse.Parser, path string, out map[string]bool) {
	f, diags := parser.ParseHCLFile(path)
	if diags.HasErrors() || f == nil {
		return
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return
	}
	for _, blk := range body.Blocks {
		if blk.Type != "variable" || len(blk.Labels) != 1 {
			continue
		}
		attr, ok := blk.Body.Attributes["ephemeral"]
		if !ok {
			continue
		}
		val, vdiags := attr.Expr.Value(nil)
		if vdiags.HasErrors() || val.IsNull() {
			continue
		}
		if !val.Type().Equals(cty.Bool) {
			continue
		}
		if val.True() {
			out[blk.Labels[0]] = true
		}
	}
}

// collectEphemeralFromJSON parses a Terraform `*.tf.json` file and adds any
// variables whose `ephemeral` field is the JSON boolean `true` to out.
//
// The Terraform JSON syntax permits the top-level `variable` key to be
// either an object keyed by variable name or an array of single-entry
// objects. Both shapes are handled.
func collectEphemeralFromJSON(path string, out map[string]bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return
	}
	raw, ok := root["variable"]
	if !ok {
		return
	}
	// Preferred shape: { "variable": { "name": {...}, ... } }
	var asObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asObject); err == nil {
		for name, body := range asObject {
			if jsonVarHasEphemeral(body) {
				out[name] = true
			}
		}
		return
	}
	// Alternate shape: { "variable": [ {"name": {...}}, ... ] }
	var asArray []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asArray); err != nil {
		return
	}
	for _, entry := range asArray {
		for name, body := range entry {
			if jsonVarHasEphemeral(body) {
				out[name] = true
			}
		}
	}
}

// jsonVarHasEphemeral reports whether the given variable body declares
// `"ephemeral": true` as a JSON boolean literal. Non-boolean values
// (strings, expressions encoded as `${...}`) are ignored to mirror the
// "constant boolean" rule used for HCL.
func jsonVarHasEphemeral(body json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return false
	}
	raw, ok := fields["ephemeral"]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false
	}
	return b
}
