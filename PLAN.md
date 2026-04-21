# Implementation plan: issue #5

Support local/go-getter sources and custom (private) registry endpoints
with bearer-token auth and remote service discovery.

## Goals

1. Accept a raw go-getter source URL (local path, `git::`, `s3::`, etc.)
   and bypass the registry lookup entirely.
2. Support a **custom** module registry (self-hosted, mirror, private)
   in addition to the two public ones.
3. Authenticate to custom registries with bearer tokens, sourced from
   explicit config, `TF_TOKEN_<host>`, or `credentials.tfrc.json` (JSON
   only).
4. Use Terraform remote service discovery so users can pass a bare host
   for `--registry-url` without knowing the API path.

## Non-goals

- Full `.terraformrc` / `terraform.rc` HCL parsing.
- `credentials_helper` external-program support.
- Honoring service-discovery `Cache-Control` TTLs (in-memory only).
- Wildcard/suffix matching of credential hosts.

## Design decisions (locked with user)

- **Request shape:** add `Source string` field to `Request`. When set,
  skip registry resolution and hand straight to downloader.
- **Local paths** (`file::` or relative): inspect in place, do NOT copy
  to cache.
- **Remote raw sources:** cache under `<cacheDir>/source/<sha256(src)[:16]>/`.
- **Version constraints with `Source`:** disallowed; `Source` must be
  concrete (go-getter `?ref=` etc. OK).
- **Registry type name:** `custom` (covers private + public mirrors).
- **Credentials host matching:** exact `host:port` first, then bare
  hostname; case-insensitive.
- **Base URL input:** accept host-only and run service discovery; also
  accept a full base with path and skip discovery.
- **Token sources supported:** explicit > `TF_TOKEN_<host>` env >
  `credentials.tfrc.json`. JSON credentials file only.
- **Credentials file locations (first hit wins):**
  1. `$TF_CLI_CONFIG_FILE` if set.
  2. `$XDG_CONFIG_HOME/terraform/credentials.tfrc.json` (if
     `XDG_CONFIG_HOME` set).
  3. `$HOME/.terraform.d/credentials.tfrc.json` (unix).
  4. `%APPDATA%/terraform.d/credentials.tfrc.json` (windows).
- **Token host scoping:** bearer header injected only when outgoing
  request host matches registry host; stripped on cross-host redirect.

## API changes

### `types.go`

```go
type RegistryType string

const (
    RegistryTypeOpenTofu  RegistryType = "opentofu"
    RegistryTypeTerraform RegistryType = "terraform"
    RegistryTypeCustom    RegistryType = "custom" // NEW
)

type Request struct {
    // ... existing fields ...

    // Source, when non-empty, is a go-getter source URL used to fetch
    // the module directly, bypassing the registry. Mutually exclusive
    // with version constraints (Version must be empty or a concrete
    // version used only for cache keying).
    Source string `json:"source,omitempty"`
}
```

### `server.go` / new files

New options:

```go
// WithCustomRegistry configures a custom registry by base URL or host.
// If baseURL is host-only (no path) service discovery is used to
// resolve the modules endpoint. The bearer token, when non-empty,
// overrides host-based token discovery.
func WithCustomRegistry(baseURL, token string) ServerOption
```

Registry package additions:

```go
// registry/custom.go
func NewCustom(baseURL string, opts ...Option) Registry

// registry/auth.go
func WithBearerToken(token string) Option

// registry/discovery.go
func DiscoverModulesEndpoint(ctx context.Context, client *http.Client, input string) (string, error)

// registry/token.go
func ResolveTokenForHost(host string) (string, error)
```

## Work breakdown (commits)

1. `feat: add Request.Source for go-getter module sources`
   - `types.go`: add `Source` field.
   - `schema.go`/`server.go`: branch in `getModule` on `Source != ""`;
     skip `resolveRequest` registry calls; inspect-in-place for local
     paths (absolute, relative, `file::`); hashed cache dir for remote.
   - `cache.go`: helper `cacheSourceDir(cacheDir, source) string` using
     `sha256(source)[:16]`.
   - Reject `Source` + version constraint.
   - Tests: local fixture under `testdata/`, remote via local `git::`
     URL against a bare repo fixture.

2. `feat: add RegistryTypeCustom with explicit base URL`
   - `registry/custom.go`: `NewCustom(baseURL, opts...)`; reuses
     `listVersions` / `resolveDownload` helpers.
   - `server.go`: wire `registryFor(RegistryTypeCustom)` — requires a
     previously-installed custom registry via option, otherwise error.
   - `WithCustomRegistry(baseURL, token)` option (discovery and auth
     added in later commits; initial version accepts full base URL
     only).
   - Tests: `httptest.Server` acting as custom registry.

3. `feat: remote service discovery for custom registry host-only input`
   - `registry/discovery.go`: `DiscoverModulesEndpoint`.
   - `WithCustomRegistry` uses discovery when input lacks a path.
   - In-memory cache on `Server` keyed by raw input URL.
   - Tests: relative and absolute `modules.v1`, 404 path, malformed
     JSON.

4. `feat: bearer-token auth for custom registry`
   - `registry/auth.go`: host-scoped `http.RoundTripper` wrapping an
     underlying transport; strips on cross-host redirect (via
     `http.Client.CheckRedirect`).
   - `registry.WithBearerToken(token)` Option; applied to the client
     used for both registry API calls and go-getter HTTP downloads to
     the same host.
   - Tests: `httptest.Server` asserting `Authorization: Bearer ...`;
     cross-host redirect strips header.

5. `feat: token discovery from TF_TOKEN_* and credentials.tfrc.json`
   - `registry/token.go`: `ResolveTokenForHost(host)` implementing
     precedence.
   - `TF_TOKEN_<host>` key derivation: dots → underscores, hyphens →
     `__` per Terraform docs.
   - `credentials.tfrc.json` parsing (JSON only).
   - Wired into `WithCustomRegistry` when caller passes empty token.
   - Tests: token-precedence, file-location fallback, no-leak-on-redirect
     (already covered in #4 but re-verify with env/file tokens).

6. `feat(cli): --source, --registry-url, --registry-token flags`
   - `cmd/tfmoduleschema/main.go`:
     - `--source` / `-S`: raw go-getter URL (mutually exclusive with
       `--namespace`/`--name`/`--system`).
     - `--registry-url`: custom registry base or host; implies
       `--registry=custom`.
     - `--registry-token` (env `TFMODULESCHEMA_REGISTRY_TOKEN`):
       explicit token override.
   - Validation: require either `--source` or the ns/name/system trio;
     not both.
   - Integration test covering CLI invocation with `--source` on a
     local fixture.

7. `docs: README sections for local sources and custom registries`
   - New sections with copy-pasteable examples.
   - Explicit note: JSON credentials file only; HCL `.terraformrc` not
     parsed.
   - Document `TF_TOKEN_<host>` key-derivation rules with examples.

## Test matrix summary

| Feature | Unit test | Notes |
|---|---|---|
| `Source` local path | yes | fixture under `testdata/source-local/` |
| `Source` remote (git) | yes | local bare repo via `git::file://` |
| `Source` + version constraint rejected | yes | error contains useful msg |
| Custom registry basic flow | yes | httptest server |
| Service discovery: relative `modules.v1` | yes | |
| Service discovery: absolute `modules.v1` | yes | |
| Service discovery: 404 / bad JSON | yes | |
| Bearer injection on same host | yes | |
| Bearer stripped on cross-host redirect | yes | |
| `TF_TOKEN_<host>` lookup | yes | |
| `credentials.tfrc.json` lookup | yes | |
| Token precedence | yes | explicit > env > file |
| CLI `--source` | yes | |
| CLI `--registry-url` + `--registry-token` | yes | |

## Risks / open items

- `go-getter/v2` HTTP getter customization — confirm the `Getters` map
  override pattern still works in v2 (the public API changed from v1).
  Fallback: pre-resolve download URL via registry client with our
  auth'd http.Client, then hand the redirected URL (if any) to
  go-getter unauthenticated.
- TLS to internal hosts with self-signed certs — out of scope for now;
  users can set `GODEBUG` or use a custom `http.Client` via existing
  `WithHTTPClient`.
- Credentials file precedence between `XDG_CONFIG_HOME` and
  `~/.terraform.d/` — Terraform itself is not entirely consistent
  here. Document chosen order.
