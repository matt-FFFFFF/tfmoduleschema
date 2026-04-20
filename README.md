# tfmoduleschema

A Go library and CLI that returns the **variables, outputs, required
providers, available versions, and submodules** for any Terraform module
published on the [OpenTofu] or [HashiCorp Terraform] module registries.

Sibling project to [`tfpluginschema`](https://github.com/matt-FFFFFF/tfpluginschema),
but for modules instead of providers.

[OpenTofu]: https://registry.opentofu.org
[HashiCorp Terraform]: https://registry.terraform.io

## Why

The public OpenTofu registry implements only the minimal
[module registry protocol](https://opentofu.org/docs/internals/module-registry-protocol/)
(`/versions` and `/download`) and does not expose the rich `inputs`, `outputs`,
and `submodules` JSON that the HashiCorp registry surfaces. To work the same
way against either registry, `tfmoduleschema` always resolves the module's
download URL, fetches the source with [`go-getter`], and parses the HCL with
[`terraform-config-inspect`]. This yields a consistent schema no matter which
registry you point at.

[`go-getter`]: https://github.com/hashicorp/go-getter
[`terraform-config-inspect`]: https://github.com/hashicorp/terraform-config-inspect

## Features

- Fetches module metadata from either the OpenTofu or HashiCorp registry.
- Returns **variables**, **outputs**, **required_providers**,
  **required_core**, **managed/data resources**, **module calls**, and
  **diagnostics** for both the root module and any submodule.
- Lists available versions and resolves version constraints (`~> 1.2`,
  `>= 3.0, < 4.0`) to a concrete version.
- Persistent, content-addressable on-disk cache (reused across runs).
- JSON output only.
- CLI and library APIs.

## Installation

Library:

```bash
go get github.com/matt-FFFFFF/tfmoduleschema
```

CLI:

```bash
go install github.com/matt-FFFFFF/tfmoduleschema/cmd/tfmoduleschema@latest
```

Prebuilt binaries for Linux, macOS, and Windows are published on each
[GitHub Release](https://github.com/matt-FFFFFF/tfmoduleschema/releases).

## Library quick start

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"

    "github.com/matt-FFFFFF/tfmoduleschema"
)

func main() {
    s := tfmoduleschema.NewServer(nil)
    defer s.Cleanup()

    req := tfmoduleschema.Request{
        Namespace: "terraform-aws-modules",
        Name:      "vpc",
        System:    "aws",
        Version:   "5.13.0", // empty = latest
    }

    m, err := s.GetModule(context.Background(), req)
    if err != nil {
        log.Fatal(err)
    }
    b, _ := json.MarshalIndent(m, "", "  ")
    fmt.Println(string(b))
}
```

### Selecting a registry

Set `Request.RegistryType` to switch between registries. The default is
`RegistryTypeOpenTofu`.

```go
req := tfmoduleschema.Request{
    Namespace:    "terraform-aws-modules",
    Name:         "vpc",
    System:       "aws",
    Version:      "5.13.0",
    RegistryType: tfmoduleschema.RegistryTypeTerraform,
}
```

### Public API

`NewServer(l *slog.Logger, opts ...ServerOption) *Server` builds a
server. All `Server` methods take a `context.Context` and a `Request` (or
`VersionsRequest`).

| Method | Purpose |
|---|---|
| `GetModule(ctx, req)` | Parsed root module (`*Module`). |
| `GetSubmodule(ctx, req, subpath)` | Parsed submodule at `subpath` (e.g. `"modules/network"`). |
| `ListSubmodules(ctx, req)` | Paths of first-level submodules under `modules/`. |
| `GetVariables(ctx, req)` | Convenience: just the root variables. |
| `GetOutputs(ctx, req)` | Convenience: just the root outputs. |
| `GetProviderRequirements(ctx, req)` | Convenience: just the root `required_providers` map. |
| `GetAvailableVersions(ctx, vreq)` | Sorted `go-version.Collection` of all published versions. |

### Options

- `WithCacheDir(dir)` — override the cache directory.
- `WithForceFetch(true)` — always re-download, bypassing the cache.
- `WithHTTPClient(c)` — supply a custom `*http.Client` for registry calls.
- `WithCacheStatusFunc(fn)` — callback invoked with
  `(Request, CacheStatus)` after each resolution.
- `WithRegistry(t, r)` — inject a custom `registry.Registry` implementation
  (handy for tests or private registries).

### Versions

`Request.Version` may be a concrete version (`"5.13.0"`), a constraint
(`"~> 5.13"`, `">= 5.0, < 6.0"`), or empty for "latest stable". Version
selection uses `hashicorp/go-version` semantics.

## CLI

```
tfmoduleschema --ns <namespace> -n <name> -s <system> \
  [--version-constraint VERSION] [--registry opentofu|terraform] \
  <command>
```

Global flags:

| Flag | Short | Description |
|---|---|---|
| `--namespace` | `--ns` | Module namespace (required). |
| `--name` | `-n` | Module name (required). |
| `--system` | `-s` | Target system / "provider" (required). |
| `--version-constraint` | `--vc` | Concrete version or constraint. Empty = latest. |
| `--registry` | `-r` | `opentofu` (default) or `terraform`. |
| `--cache-dir` | | Cache directory. Overrides `$TFMODULESCHEMA_CACHE_DIR`. |
| `--force-fetch` | | Always re-download. |
| `--quiet` | | Suppress `hit:` / `miss:` status on stderr. |

Commands:

| Command | Description |
|---|---|
| `module schema` | Full parsed root module as JSON. |
| `variable list` | Newline-separated variable names. |
| `variable schema [name]` | Full schema for one variable, or all. |
| `output list` | Newline-separated output names. |
| `output schema [name]` | Full schema for one output, or all. |
| `provider list` | Newline-separated required-provider names. |
| `provider schema [name]` | Full requirement for one provider, or the map. |
| `submodule list` | Paths of first-level submodules. |
| `submodule schema <path>` | Full schema for one submodule. |
| `version list` | All versions the registry advertises. |

### Examples

```bash
# List versions (OpenTofu registry by default).
tfmoduleschema --ns terraform-aws-modules -n vpc -s aws version list

# Full root-module schema, pinned version.
tfmoduleschema --ns terraform-aws-modules -n vpc -s aws --vc 5.13.0 module schema

# Just the variable names for the latest stable version.
tfmoduleschema --ns terraform-aws-modules -n vpc -s aws variable list

# Schema for one variable.
tfmoduleschema --ns terraform-aws-modules -n vpc -s aws --vc 5.13.0 \
  variable schema cidr

# Use the HashiCorp registry.
tfmoduleschema -r terraform --ns Azure -n avm-res-compute-virtualmachine \
  -s azurerm variable list

# Inspect a submodule.
tfmoduleschema --ns terraform-aws-modules -n vpc -s aws --vc 5.13.0 \
  submodule schema modules/vpc-endpoints
```

## Caching

### On-disk layout

Downloaded modules are extracted into a registry-qualified path:

```
<cacheDir>/<registry-type>/<namespace>/<name>/<system>/<version>/
```

Including the registry type avoids collisions between modules with the
same coordinates published on different registries.

The default `<cacheDir>` is `os.UserCacheDir()/tfmoduleschema` (for
example `~/Library/Caches/tfmoduleschema` on macOS and
`~/.cache/tfmoduleschema` on Linux). It can be overridden with:

- The `TFMODULESCHEMA_CACHE_DIR` environment variable.
- The `--cache-dir` CLI flag.
- The `tfmoduleschema.WithCacheDir(dir)` option.

Downloads are staged to `<dest>.partial` and atomically renamed into
place, so an interrupted download never leaves a half-populated cache
entry.

### Bypassing the cache

- `--force-fetch` on the CLI.
- `tfmoduleschema.WithForceFetch(true)` as a `NewServer` option.

### Observing cache hits / misses

The CLI prints `cache hit:` / `downloading:` to stderr for each request
(`--quiet` to suppress). Library users can register a callback:

```go
s := tfmoduleschema.NewServer(nil,
    tfmoduleschema.WithCacheStatusFunc(func(req tfmoduleschema.Request, st tfmoduleschema.CacheStatus) {
        log.Printf("%s: %s/%s/%s@%s", st, req.Namespace, req.Name, req.System, req.Version)
    }),
)
```

## Testing

```bash
go test -short ./...   # unit tests only
go test ./...          # also runs tests that hit the public registries
```

The integration tests pin a published module version and exercise both
registries end-to-end.

## License

[MPL-2.0](LICENSE).
