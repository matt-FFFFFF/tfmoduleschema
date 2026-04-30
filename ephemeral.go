package tfmoduleschema

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ephemeralVariables walks every *.tf file in dir (non-recursive) and returns
// the set of variable names declared with `ephemeral = true`.
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
		if !strings.HasSuffix(name, ".tf") {
			continue
		}
		// Skip override files; complexity not worth it for an attribute as
		// rarely overridden as `ephemeral`.
		if strings.HasSuffix(name, "_override.tf") || name == "override.tf" {
			continue
		}
		path := filepath.Join(dir, name)
		f, diags := parser.ParseHCLFile(path)
		if diags.HasErrors() || f == nil {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
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
	return out
}
