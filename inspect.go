package tfmoduleschema

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// inspectDir parses the Terraform module rooted at dir using
// terraform-config-inspect and maps the result into our Module type.
// subpath, if non-empty, is recorded on the resulting Module's Path field
// so callers can distinguish root vs submodule.
func inspectDir(dir, subpath string) (*Module, error) {
	if _, err := statDir(dir); err != nil {
		return nil, err
	}
	m, diags := tfconfig.LoadModule(dir)
	if m == nil {
		// LoadModule always returns a non-nil module, but be defensive.
		m = tfconfig.NewModule(dir)
	}
	eph := ephemeralVariables(dir)
	return mapModule(m, subpath, diags, eph), nil
}

// mapModule converts a tfconfig.Module into our Module type.
// ephemeral is a name-set of variables declared with `ephemeral = true`,
// since terraform-config-inspect does not surface that attribute.
func mapModule(m *tfconfig.Module, subpath string, diags tfconfig.Diagnostics, ephemeral map[string]bool) *Module {
	out := &Module{
		Path:              subpath,
		RequiredCore:      append([]string(nil), m.RequiredCore...),
		RequiredProviders: make(map[string]ProviderRequirement, len(m.RequiredProviders)),
	}

	// Variables (stable order by name).
	varNames := make([]string, 0, len(m.Variables))
	for n := range m.Variables {
		varNames = append(varNames, n)
	}
	sort.Strings(varNames)
	out.Variables = make([]Variable, 0, len(varNames))
	for _, n := range varNames {
		v := m.Variables[n]
		out.Variables = append(out.Variables, Variable{
			Name:        v.Name,
			Type:        v.Type,
			Description: v.Description,
			Default:     v.Default,
			Required:    v.Required,
			Sensitive:   v.Sensitive,
			Ephemeral:   ephemeral[v.Name],
			Pos:         SourcePos{Filename: v.Pos.Filename, Line: v.Pos.Line},
		})
	}

	// Outputs.
	outNames := make([]string, 0, len(m.Outputs))
	for n := range m.Outputs {
		outNames = append(outNames, n)
	}
	sort.Strings(outNames)
	out.Outputs = make([]Output, 0, len(outNames))
	for _, n := range outNames {
		o := m.Outputs[n]
		out.Outputs = append(out.Outputs, Output{
			Name:        o.Name,
			Description: o.Description,
			Sensitive:   o.Sensitive,
			Pos:         SourcePos{Filename: o.Pos.Filename, Line: o.Pos.Line},
		})
	}

	// Required providers.
	for name, pr := range m.RequiredProviders {
		if pr == nil {
			continue
		}
		aliases := make([]string, 0, len(pr.ConfigurationAliases))
		for _, a := range pr.ConfigurationAliases {
			if a.Alias != "" {
				aliases = append(aliases, fmt.Sprintf("%s.%s", a.Name, a.Alias))
			} else {
				aliases = append(aliases, a.Name)
			}
		}
		out.RequiredProviders[name] = ProviderRequirement{
			Source:               pr.Source,
			VersionConstraints:   append([]string(nil), pr.VersionConstraints...),
			ConfigurationAliases: aliases,
		}
	}

	// Managed & data resources.
	for _, r := range m.ManagedResources {
		out.ManagedResources = append(out.ManagedResources, Resource{
			Mode:     "managed",
			Type:     r.Type,
			Name:     r.Name,
			Provider: providerRefString(r.Provider),
			Pos:      SourcePos{Filename: r.Pos.Filename, Line: r.Pos.Line},
		})
	}
	sort.Slice(out.ManagedResources, func(i, j int) bool { return out.ManagedResources[i].Type+out.ManagedResources[i].Name < out.ManagedResources[j].Type+out.ManagedResources[j].Name })
	for _, r := range m.DataResources {
		out.DataResources = append(out.DataResources, Resource{
			Mode:     "data",
			Type:     r.Type,
			Name:     r.Name,
			Provider: providerRefString(r.Provider),
			Pos:      SourcePos{Filename: r.Pos.Filename, Line: r.Pos.Line},
		})
	}
	sort.Slice(out.DataResources, func(i, j int) bool { return out.DataResources[i].Type+out.DataResources[i].Name < out.DataResources[j].Type+out.DataResources[j].Name })

	// Module calls (map preserved).
	if len(m.ModuleCalls) > 0 {
		out.ModuleCalls = make(map[string]ModuleCall, len(m.ModuleCalls))
		for name, mc := range m.ModuleCalls {
			out.ModuleCalls[name] = ModuleCall{
				Name:    mc.Name,
				Source:  mc.Source,
				Version: mc.Version,
				Pos:     SourcePos{Filename: mc.Pos.Filename, Line: mc.Pos.Line},
			}
		}
	}

	// Diagnostics.
	for _, d := range diags {
		sp := SourcePos{}
		if d.Pos != nil {
			sp = SourcePos{Filename: d.Pos.Filename, Line: d.Pos.Line}
		}
		out.Diagnostics = append(out.Diagnostics, Diagnostic{
			Severity: severityString(d.Severity),
			Summary:  d.Summary,
			Detail:   d.Detail,
			Pos:      sp,
		})
	}

	return out
}

func providerRefString(p tfconfig.ProviderRef) string {
	if p.Alias != "" {
		return p.Name + "." + p.Alias
	}
	return p.Name
}

func severityString(s tfconfig.DiagSeverity) string {
	switch s {
	case tfconfig.DiagError:
		return "error"
	case tfconfig.DiagWarning:
		return "warning"
	default:
		return "info"
	}
}

// listSubmoduleDirs returns the relative paths of first-level directories
// under <moduleRoot>/modules that look like Terraform modules.
func listSubmoduleDirs(moduleRoot string) ([]string, error) {
	modsDir := filepath.Join(moduleRoot, "modules")
	entries, err := readDir(modsDir)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading submodules dir: %w", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(modsDir, e.Name())
		if tfconfig.IsModuleDir(sub) {
			out = append(out, "modules/"+e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// resolveSubmodulePath returns the absolute path on disk of the submodule
// identified by subpath relative to moduleRoot. It rejects paths that
// escape the module root via "..".
func resolveSubmodulePath(moduleRoot, subpath string) (string, error) {
	clean := filepath.Clean(subpath)
	if clean == "." || clean == "" {
		return moduleRoot, nil
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("submodule path must be relative and within the module root: %q", subpath)
	}
	full := filepath.Join(moduleRoot, clean)
	rel, err := filepath.Rel(moduleRoot, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("submodule path escapes module root: %q", subpath)
	}
	return full, nil
}
