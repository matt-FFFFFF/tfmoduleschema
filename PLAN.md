# Plan: `tfmoduleschema`

A Go library + CLI that returns **variables, outputs, required providers, versions,
and submodules** for any Terraform module published on the OpenTofu or Terraform
(HashiCorp) registries. Mirrors the structure and conventions of
[`tfpluginschema`](https://github.com/matt-FFFFFF/tfpluginschema), but for
modules rather than providers.

## Goals

- Fetch module metadata from either the OpenTofu registry
  (`registry.opentofu.org`) or the HashiCorp Terraform registry
  (`registry.terraform.io`).
- Expose, per module:
  - Input variables
  - Outputs
  - Required providers (including version constraints)
  - Available versions
  - Submodules (paths, and per-submodule metadata)
- Provide both a Go library (`package tfmoduleschema`) and a CLI
  (`cmd/tfmoduleschema`) with JSON output.
- In-memory + on-disk caching of downloaded module sources.

## Decisions (settled)

| Decision | Choice | Rationale |
|---|---|---|
| Data source | Always download module source and parse locally | Universal across registries (OpenTofu's public registry does not expose rich module metadata; only the minimal protocol); higher fidelity than registry summary JSON; one code path. |
| Download library | `github.com/hashicorp/go-getter/v2` | Handles `git::`, `https`, tarballs, `s3::`, `gcs::`, subdir syntax — same as Terraform CLI. |
| HCL parsing | `github.com/hashicorp/terraform-config-inspect` | Purpose-built for this; used by `terraform-docs` and the Terraform registry. Extracts vars/outputs/required_providers/resources/module-calls. |
| CLI framework | `github.com/urfave/cli/v3` | Consistent with `tfpluginschema`. |
| Version lib | `github.com/hashicorp/go-version` | Consistent with `tfpluginschema`. |
| Output | JSON only | Consistent with `tfpluginschema`. |
| Submodule addressing | Path relative to module root (e.g. `modules/network`) | Matches registry convention and is unambiguous. |
| Service discovery (`.well-known/terraform.json`) | **Deferred to v0.2** | v0.1 hardcodes the two known public registries. |

## Key research finding

The **OpenTofu public registry implements only the minimal module-registry
protocol** — `versions` and `download` endpoints. It does NOT expose the rich
`inputs` / `outputs` / `submodules` JSON that `registry.terraform.io` does. So
"always download and parse" is the only strategy that produces a consistent
result across both registries (and future private registries).

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                   CLI  (cmd/tfmoduleschema)                 │
│                        urfave/cli/v3                        │
└─────────────────────────────────────────────────────────────┘
                              │
┌─────────────────────────────▼───────────────────────────────┐
│                     Server  (package)                       │
│  • orchestrates resolve → download → inspect → cache        │
│  • in-memory + on-disk cache                                │
│  • exposes Variables/Outputs/Providers/Versions/Submodules  │
└──────────────┬────────────────────────────┬─────────────────┘
               │                            │
     ┌─────────▼─────────┐          ┌───────▼────────────┐
     │ Registry (iface)  │          │  Inspector         │
     │  ListVersions     │          │  parses .tf files  │
     │  ResolveDownload  │          │  (terraform-       │
     │                   │          │   config-inspect)  │
     │  impls:           │          └────────────────────┘
     │   • opentofu      │
     │   • terraform     │
     └─────────┬─────────┘
               │
     ┌─────────▼─────────┐
     │  Downloader       │
     │  go-getter v2     │
     └───────────────────┘
```

## Package layout

```
/
├── go.mod                              # module github.com/matt-FFFFFF/tfmoduleschema
├── Makefile
├── README.md
├── LICENSE
├── PLAN.md
├── .goreleaser.yaml
├── .github/workflows/{go-test,release}.yml
│
├── server.go           # Server, Request, NewServer, functional options
├── cache.go            # on-disk cache paths, CacheStatus, cache options
├── versions.go         # ListVersions + constraint resolution (fixVersion)
├── schema.go           # GetModule/GetVariables/GetOutputs/GetProviders/GetSubmodule/ListSubmodules
├── inspect.go          # wraps terraform-config-inspect → our Module type
├── download.go         # go-getter v2 wrapper (atomic rename into cache)
├── types.go            # Module, Variable, Output, ProviderRequirement, Resource, ModuleCall, Diagnostic, SourcePos
│
├── registry/
│   ├── registry.go     # Registry interface + VersionsRequest
│   ├── opentofu.go     # OpenTofu client
│   └── terraform.go    # HashiCorp Terraform client
│
├── cmd/tfmoduleschema/
│   └── main.go
│
├── testdata/           # fixture module trees for inspect tests (incl. submodules)
└── *_test.go           # unit + integration tests (both registries)
```

## Core types

```go
// Request identifies a module version to inspect.
type Request struct {
    Namespace    string       // e.g. "Azure"
    Name         string       // e.g. "avm-res-compute-virtualmachine"
    System       string       // e.g. "azurerm" (called "provider" in the Hashi API)
    Version      string       // fixed version or constraint; "" = latest
    RegistryType RegistryType // defaults to OpenTofu
}

type VersionsRequest struct {
    Namespace, Name, System string
    RegistryType            RegistryType
}

type RegistryType string
const (
    RegistryTypeOpenTofu  RegistryType = "opentofu"  // registry.opentofu.org
    RegistryTypeTerraform RegistryType = "terraform" // registry.terraform.io
)

// Registry abstracts the two HTTP APIs.
type Registry interface {
    BaseURL() string
    ListVersions(ctx context.Context, r VersionsRequest) (goversion.Collection, error)
    // ResolveDownload returns a go-getter-compatible source URL for the given version.
    ResolveDownload(ctx context.Context, r Request) (string, error)
}
```

### Result types (JSON-tagged, package-local)

```go
type Module struct {
    Path              string                          // "" for root, "modules/foo" for submodules
    Variables         []Variable
    Outputs           []Output
    RequiredCore      []string
    RequiredProviders map[string]ProviderRequirement
    ManagedResources  []Resource
    DataResources     []Resource
    ModuleCalls       map[string]ModuleCall
    Diagnostics       []Diagnostic
}

type Variable struct {
    Name        string
    Type        string  // raw HCL type expression
    Description string
    Default     any     // JSON-safe
    Sensitive   bool
    Nullable    *bool
    Pos         SourcePos
}

type Output struct {
    Name        string
    Description string
    Sensitive   bool
    Pos         SourcePos
}

type ProviderRequirement struct {
    Source               string
    VersionConstraints   []string
    ConfigurationAliases []string
}

type Resource   struct { Type, Name, Provider string; Pos SourcePos }
type ModuleCall struct { Name, Source, Version string; Pos SourcePos }
type Diagnostic struct { Severity, Summary, Detail string; Pos SourcePos }
type SourcePos  struct { Filename string; Line, Column int }
```

## Server API

```go
func NewServer(l *slog.Logger, opts ...ServerOption) *Server

// Options (functional, mirroring tfpluginschema):
//   WithCacheDir(string)
//   WithForceFetch(bool)
//   WithHTTPClient(*http.Client)
//   WithCacheStatusFunc(CacheStatusFunc)
//   WithRegistry(RegistryType, Registry)   // inject custom registry (tests / private)

func (*Server) GetModule(Request) (*Module, error)                            // root module
func (*Server) GetSubmodule(Request, path string) (*Module, error)            // e.g. "modules/foo"
func (*Server) ListSubmodules(Request) ([]string, error)                      // paths under modules/
func (*Server) GetVariables(Request) ([]Variable, error)
func (*Server) GetOutputs(Request) ([]Output, error)
func (*Server) GetProviderRequirements(Request) (map[string]ProviderRequirement, error)
func (*Server) GetAvailableVersions(VersionsRequest) (goversion.Collection, error)
func (*Server) Cleanup() error
```

## Fetch / cache flow

```
GetModule(req)
 ├─ normalise + validate req (reject path-traversal in ns/name/system)
 ├─ resolve version (fixVersion):
 │   if not a concrete goversion → ListVersions → pick newest matching constraint
 ├─ check in-memory moduleCache keyed by (registry, ns, name, system, version)
 ├─ check on-disk cache:
 │     <cacheDir>/<registry>/<ns>/<name>/<system>/<version>/
 │   if present → inspect and return
 ├─ Registry.ResolveDownload(req) → getter URL (e.g. "git::https://github.com/.../?ref=vX")
 ├─ go-getter v2 → download+extract into "<cache>.partial" → atomic os.Rename
 ├─ terraform-config-inspect.LoadModule(rootPath) → tfconfig.Module
 ├─ map tfconfig.Module → our Module type
 └─ cache + return
```

`GetSubmodule(req, "modules/foo")` runs inspect against
`<cached>/modules/foo`, constrained to stay inside the cached tree
(path-traversal protection).

`ListSubmodules` walks the first level of `modules/` by default.

## Registry implementations

Both clients share the base-path pattern `{base}/{ns}/{name}/{system}/...`.

### `registry/terraform.go` — HashiCorp (`registry.terraform.io/v1/modules`)

- `ListVersions`: `GET .../versions` → `modules[0].versions[*].version`.
- `ResolveDownload`: `GET .../{version}/download`
  - Typically `204 No Content`; read `X-Terraform-Get` header.
  - Fallback: JSON body `{"location": "..."}` (for parity).

### `registry/opentofu.go` — OpenTofu (`registry.opentofu.org/v1/modules`)

- `ListVersions`: same shape.
- `ResolveDownload`: typically `200 OK` with body `{"location": "..."}`.
  - Fallback: `X-Terraform-Get` header (to match Terraform CLI behaviour).

Relative URLs are resolved against the endpoint URL in both clients.

## CLI (mirrors `tfpluginschema` style)

```
tfmoduleschema [global flags] <command> [subcommand] [args]

Global flags:
  --namespace,       -n   (required)
  --name,            -m   (required)
  --system,          -s   (required; e.g. "aws", "azurerm")
  --module-version, -mv   (optional; version or constraint)
  --registry,        -r   "opentofu" (default) | "terraform"
  --cache-dir             (env: TFMODULESCHEMA_CACHE_DIR)
  --force-fetch
  --quiet

Commands:
  module       schema                    # full Module for root
  variables    list | schema <name>
  outputs      list | schema <name>
  providers    list | schema <name>
  submodules   list | schema <path>
  version      list
```

Output: JSON via `json.Encoder` with `SetIndent("", "  ")`. Cache hit/miss
messages go to stderr (suppressible with `--quiet`), matching
`tfpluginschema`.

## Dependencies

| Dep | Purpose |
|---|---|
| `github.com/hashicorp/terraform-config-inspect` | Parse `.tf`; extract vars/outputs/required_providers/resources/module-calls. |
| `github.com/hashicorp/go-getter/v2` | Download git/tarball/etc. source returned by the registry. |
| `github.com/hashicorp/go-version` | Version parsing + constraint matching. |
| `github.com/urfave/cli/v3` | CLI framework. |
| `github.com/stretchr/testify` | Test assertions. |

No gRPC, no `go-plugin`, no protobuf — materially lighter than `tfpluginschema`.

## Testing strategy

- **Unit**: table-driven tests per registry client using `httptest.Server`
  (both success and edge cases like 204 vs 200, header vs body, relative URL
  resolution).
- **Fixtures**: small `.tf` module trees under `testdata/`, including one with
  `modules/*` submodules, covering variables with `type`, `default`,
  `sensitive`, `nullable`, and outputs with `sensitive`.
- **Integration** (network, both registries): fetch a known module
  (e.g. `Azure/avm-res-compute-virtualmachine/azurerm` and
  `terraform-aws-modules/vpc/aws`) against both `opentofu` and `terraform`
  and assert shape.
- **Examples** (`*_example_test.go`) doubling as godoc-runnable examples.

## On-disk cache layout

```
<cacheDir>/<registry>/<namespace>/<name>/<system>/<version>/
```

Atomic population via `<path>.partial` → `os.Rename` (same pattern as
`tfpluginschema`).

Env var for default cache dir: `TFMODULESCHEMA_CACHE_DIR`.

## Implementation order

1. Scaffold: `go.mod`, `Makefile`, `.gitignore`, `.github/workflows/go-test.yml`, `LICENSE`, skeleton `README.md`.
2. `types.go` + `registry/` interface and two clients, with `httptest` tests.
3. `versions.go` + constraint resolution.
4. `download.go` (go-getter wrapper) + `cache.go` (path layout, atomic rename).
5. `inspect.go` + `schema.go` (tfconfig → our types) with fixture tests.
6. `server.go` wiring + public methods.
7. CLI (`cmd/tfmoduleschema/main.go`).
8. Integration tests, examples, full README, `.goreleaser.yaml`, release workflow.

## Out of scope for v0.1 (future work)

- `.well-known/terraform.json` service discovery for arbitrary hostnames /
  private registries / Terraform Enterprise.
- Authentication (registry tokens) — not needed for the two public registries.
- Non-JSON output formats.
- Resolving nested `source = "../other"` module references beyond what
  `terraform-config-inspect` surfaces as `ModuleCalls`.
