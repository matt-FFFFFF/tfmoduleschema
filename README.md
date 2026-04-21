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

- Fetches module metadata from the OpenTofu, HashiCorp, or any custom
  (private/mirror) Terraform module registry.
- Also inspects modules from **local paths** and arbitrary
  [`go-getter`] sources (git, S3, HTTP archive, etc.), so you can schema
  a module that isn't published yet.
- Returns **variables**, **outputs**, **required_providers**,
  **required_core**, **managed/data resources**, **module calls**, and
  **diagnostics** for both the root module and any submodule.
- Lists available versions and resolves version constraints (`~> 1.2`,
  `>= 3.0, < 4.0`) to a concrete version.
- Persistent, content-addressable on-disk cache (reused across runs).
- Bearer-token auth for private registries, with the same
  `TF_TOKEN_<host>` / `credentials.tfrc.json` discovery Terraform uses.
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
- `WithCustomRegistry(input, opts...)` — install a private / mirror /
  self-hosted Terraform module registry. See
  [Private and custom registries](#private-and-custom-registries).

### Local and custom sources

Set `Request.Source` to inspect a module that isn't in a registry.
Anything [`go-getter`] accepts is accepted here: local paths (absolute
or relative), `file://` URLs, `git::https://...`, `s3::...`,
`https://.../archive.tar.gz`, etc.

```go
m, err := s.GetModule(ctx, tfmoduleschema.Request{
    Source: "./modules/vpc",
})
```

Rules:

- `Source` is mutually exclusive with `Namespace`/`Name`/`System`.
- Local paths are inspected **in place** — nothing is copied into the
  cache. Edits are picked up on the next call.
- Remote sources are fetched via `go-getter` and cached under
  `<cacheDir>/source/<sha256(src)[:16]>/`. Pin a ref in the source URL
  (e.g. `?ref=v1.2.3`) so the cache key changes when the source does.
- Version constraints cannot be combined with `Source`. If you supply
  a non-concrete `Version`, the call errors with `ErrSourceWithConstraint`.

### Private and custom registries

`WithCustomRegistry` configures a custom registry for `Request`s that
use `RegistryType: RegistryTypeCustom`:

```go
s := tfmoduleschema.NewServer(nil,
    tfmoduleschema.WithCustomRegistry("registry.example.com",
        registry.WithBearerToken("secret"), // optional
    ),
)

m, err := s.GetModule(ctx, tfmoduleschema.Request{
    Namespace:    "infra",
    Name:         "vpc",
    System:       "aws",
    Version:      "1.2.3",
    RegistryType: tfmoduleschema.RegistryTypeCustom,
})
```

The `input` accepts any of:

- A bare host: `"registry.example.com"`.
- A host with port: `"registry.internal:8443"`.
- A URL without a path: `"https://registry.example.com"`.
- A full URL with a path: `"https://registry.example.com/v1/modules"`.

For the first three, `tfmoduleschema` performs
[Terraform remote service discovery] against
`https://<host>/.well-known/terraform.json` and resolves the
`modules.v1` endpoint. For the last form, discovery is skipped and the
URL is used verbatim.

[Terraform remote service discovery]: https://developer.hashicorp.com/terraform/internals/remote-service-discovery

The **input host** (not the discovered endpoint) is used for cache
keying and credential lookup, so pointing `--registry-url` at a
different host reliably invalidates the cache.

#### Bearer tokens

Tokens are resolved in the following order (first hit wins):

1. **Explicit** — `registry.WithBearerToken(token)` or, on the CLI,
   `--registry-token` / `$TFMODULESCHEMA_REGISTRY_TOKEN`.
2. **`TF_TOKEN_<host>` environment variable**. The host is encoded
   following Terraform's rules:
   - `.` → `_`
   - `-` → `__` (double underscore)
   - `:` (in `host:port`) → `_`

   So `registry.example.com` becomes `TF_TOKEN_registry_example_com`,
   and `my-registry.internal:8443` becomes
   `TF_TOKEN_my__registry_internal_8443`. If the `host:port` form is
   not set, the bare host is tried as a fallback.
3. **`credentials.tfrc.json`** — the JSON Terraform credentials file.
   Searched in order: `$TF_CLI_CONFIG_FILE`,
   `$XDG_CONFIG_HOME/terraform/credentials.tfrc.json`,
   `%APPDATA%/terraform.d/credentials.tfrc.json` (Windows),
   `$HOME/.terraform.d/credentials.tfrc.json`.

   Only the **JSON** form is parsed; the legacy HCL `.terraformrc`
   format is not supported. The document shape is:

   ```json
   {
     "credentials": {
       "registry.example.com": { "token": "..." }
     }
   }
   ```

The Authorization header is only sent to the configured registry host
and is stripped on cross-host redirects (e.g. to a signed S3 download
URL), so tokens are never leaked to third parties.

### Versions

`Request.Version` may be a concrete version (`"5.13.0"`), a constraint
(`"~> 5.13"`, `">= 5.0, < 6.0"`), or empty for "latest stable". Version
selection uses `hashicorp/go-version` semantics.

## CLI

```
tfmoduleschema --ns <namespace> -n <name> -s <system> \
  [--version-constraint VERSION] \
  [--registry opentofu|terraform|custom] \
  [--registry-url HOST_OR_URL [--registry-token TOKEN]] \
  <command>

# Or, inspect a local/go-getter source directly:
tfmoduleschema --source <path-or-url> <command>
```

Global flags:

| Flag | Alias | Description |
|---|---|---|
| `--namespace` | `--ns` | Module namespace. Required unless `--source` is set. |
| `--name` | `-n` | Module name. Required unless `--source` is set. |
| `--system` | `-s` | Target system / "provider". Required unless `--source` is set. |
| `--version-constraint` | `--vc` | Concrete version or constraint. Empty = latest. Must be concrete or empty when `--source` is set. |
| `--submodule` | `--sm` | Target a submodule by path (e.g. `modules/network`) instead of the root module. |
| `--source` | | Local path or `go-getter` source. Mutually exclusive with `--namespace`/`--name`/`--system`. |
| `--registry` | `-r` | `opentofu` (default), `terraform`, or `custom`. |
| `--registry-url` | | Custom registry host or base URL. Implies `--registry=custom`. |
| `--registry-token` | | Bearer token for the custom registry. Also read from `$TFMODULESCHEMA_REGISTRY_TOKEN`. Overrides `TF_TOKEN_<host>` and `credentials.tfrc.json`. |
| `--cache-dir` | | Cache directory. Overrides `$TFMODULESCHEMA_CACHE_DIR`. |
| `--force-fetch` | | Always re-download. |
| `--quiet` | | Suppress `hit:` / `miss:` status on stderr. |

Commands:

| Command | Description |
|---|---|
| `module schema` | Full parsed module as JSON (root, or submodule via `--submodule`). |
| `variable list` | Newline-separated variable names. |
| `variable schema [name]` | Full schema for one variable, or all. |
| `output list` | Newline-separated output names. |
| `output schema [name]` | Full schema for one output, or all. |
| `provider list` | Newline-separated required-provider names. |
| `provider schema [name]` | Full requirement for one provider, or the map. |
| `submodule list` | Paths of first-level submodules. |
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
  --submodule modules/vpc-endpoints module schema

# Any noun command can be retargeted at a submodule with --submodule / --sm.
tfmoduleschema --ns terraform-aws-modules -n vpc -s aws --vc 5.13.0 \
  --sm modules/vpc-endpoints variable list

tfmoduleschema --ns terraform-aws-modules -n vpc -s aws --vc 5.13.0 \
  --sm modules/vpc-endpoints output schema vpc_endpoints

# Inspect a local module directly — no registry involved.
tfmoduleschema --source ./modules/vpc variable list

# Inspect a module from a git tag.
tfmoduleschema --source 'git::https://github.com/org/repo.git//modules/vpc?ref=v1.2.3' \
  module schema

# Use a private registry. The token is resolved from
# TF_TOKEN_registry_example_com or credentials.tfrc.json if not set.
tfmoduleschema --registry-url registry.example.com \
  --ns infra -n vpc -s aws --vc 1.2.3 module schema
```

## Caching

### On-disk layout

Downloaded modules are extracted into a registry-qualified path:

```
<cacheDir>/<registry-type>/<namespace>/<name>/<system>/<version>/
```

Including the registry type avoids collisions between modules with the
same coordinates published on different registries. Custom registries
are further qualified by host:

```
<cacheDir>/custom/<sanitized-host>/<namespace>/<name>/<system>/<version>/
```

Remote `--source` / `Request.Source` fetches land under a content-keyed
directory so the same source is fetched once across runs:

```
<cacheDir>/source/<sha256(source)[:16]>/
```

Local-path sources are **not** cached; they're inspected in place.

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
