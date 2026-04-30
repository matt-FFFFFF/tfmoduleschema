// Package tfmoduleschema fetches Terraform module metadata (variables,
// outputs, required providers, available versions, and submodules) from
// either the OpenTofu or the HashiCorp Terraform module registry.
package tfmoduleschema

// RegistryType identifies which public module registry to target.
type RegistryType string

const (
	// RegistryTypeOpenTofu is the OpenTofu public registry
	// (registry.opentofu.org).
	RegistryTypeOpenTofu RegistryType = "opentofu"
	// RegistryTypeTerraform is the HashiCorp Terraform public registry
	// (registry.terraform.io).
	RegistryTypeTerraform RegistryType = "terraform"
	// RegistryTypeCustom is a user-configured module registry (a
	// private registry, mirror, or self-hosted instance). A custom
	// registry must be installed on the Server via WithCustomRegistry
	// before it can be used in a Request.
	RegistryTypeCustom RegistryType = "custom"
)

// Valid reports whether the RegistryType is one of the known values.
func (r RegistryType) Valid() bool {
	switch r {
	case RegistryTypeOpenTofu, RegistryTypeTerraform, RegistryTypeCustom:
		return true
	}
	return false
}

// Request identifies a specific module version to inspect.
type Request struct {
	// Namespace is the owning user or organisation (e.g. "Azure").
	Namespace string `json:"namespace"`
	// Name is the module name (e.g. "avm-res-compute-virtualmachine").
	Name string `json:"name"`
	// System is the target system (e.g. "azurerm"). Called "provider" in
	// the HashiCorp API.
	System string `json:"system"`
	// Version is a concrete version ("1.2.3") or a constraint ("~> 1.2").
	// An empty string selects the latest available version.
	Version string `json:"version,omitempty"`
	// RegistryType selects the registry. An empty value defaults to
	// RegistryTypeOpenTofu.
	RegistryType RegistryType `json:"registry_type,omitempty"`
	// Source, when non-empty, is a go-getter source URL used to fetch
	// the module directly, bypassing the registry. Examples:
	//
	//	/abs/path/to/module
	//	./relative/path
	//	file:///abs/path
	//	git::https://github.com/org/repo.git//modules/foo?ref=v1.2.3
	//	s3::https://s3.amazonaws.com/bucket/module.zip
	//
	// When Source is set, Namespace, Name, System, RegistryType and
	// Version are ignored for download purposes. The in-memory and
	// on-disk caches are keyed only by the Source string (and the
	// subpath for submodule lookups); the other fields do not
	// participate in cache key derivation. Version must be empty or a
	// concrete version — constraint expressions are rejected because
	// there is no registry to resolve them against. Local paths
	// (absolute, "./", "../", ".", "..", or "file://") are inspected
	// in place and are not copied into the cache.
	Source string `json:"source,omitempty"`
}

// VersionsRequest identifies a module for listing versions. It is the same
// shape as Request without a Version.
type VersionsRequest struct {
	Namespace    string       `json:"namespace"`
	Name         string       `json:"name"`
	System       string       `json:"system"`
	RegistryType RegistryType `json:"registry_type,omitempty"`
}

// Module is the parsed metadata for a single module (root or submodule).
type Module struct {
	// Path is "" for the root module, or the path relative to the module
	// root for submodules (e.g. "modules/network").
	Path              string                         `json:"path"`
	Variables         []Variable                     `json:"variables"`
	Outputs           []Output                       `json:"outputs"`
	RequiredCore      []string                       `json:"required_core,omitempty"`
	RequiredProviders map[string]ProviderRequirement `json:"required_providers"`
	ManagedResources  []Resource                     `json:"managed_resources,omitempty"`
	DataResources     []Resource                     `json:"data_resources,omitempty"`
	ModuleCalls       map[string]ModuleCall          `json:"module_calls,omitempty"`
	Diagnostics       []Diagnostic                   `json:"diagnostics,omitempty"`
}

// Variable represents an input variable declared in a module.
type Variable struct {
	Name        string    `json:"name"`
	Type        string    `json:"type,omitempty"`
	Description string    `json:"description,omitempty"`
	Default     any       `json:"default,omitempty"`
	Required    bool      `json:"required"`
	Sensitive   bool      `json:"sensitive,omitempty"`
	Ephemeral   bool      `json:"ephemeral,omitempty"`
	Pos         SourcePos `json:"pos"`
}

// Output represents an output value declared in a module.
type Output struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Sensitive   bool      `json:"sensitive,omitempty"`
	Pos         SourcePos `json:"pos"`
}

// ProviderRequirement describes a required_providers entry.
type ProviderRequirement struct {
	Source               string   `json:"source,omitempty"`
	VersionConstraints   []string `json:"version_constraints,omitempty"`
	ConfigurationAliases []string `json:"configuration_aliases,omitempty"`
}

// Resource is a managed or data resource block declared in the module.
type Resource struct {
	Mode     string    `json:"mode"` // "managed" or "data"
	Type     string    `json:"type"`
	Name     string    `json:"name"`
	Provider string    `json:"provider,omitempty"`
	Pos      SourcePos `json:"pos"`
}

// ModuleCall is a "module" block declared in the module.
type ModuleCall struct {
	Name    string    `json:"name"`
	Source  string    `json:"source"`
	Version string    `json:"version,omitempty"`
	Pos     SourcePos `json:"pos"`
}

// Diagnostic is a warning or error reported by the module parser.
type Diagnostic struct {
	Severity string    `json:"severity"`
	Summary  string    `json:"summary"`
	Detail   string    `json:"detail,omitempty"`
	Pos      SourcePos `json:"pos"`
}

// SourcePos identifies a location within a parsed .tf file.
type SourcePos struct {
	Filename string `json:"filename,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}
