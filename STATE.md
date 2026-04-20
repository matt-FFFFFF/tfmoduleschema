# STATE

Progress tracking for the implementation described in [PLAN.md](PLAN.md).

## Legend
- [x] done
- [~] in progress
- [ ] pending

## Progress

### 1. Scaffold
- [x] go.mod, Makefile, .gitignore, workflows, LICENSE, README, STATE.md

### 2. Types + registry clients
- [x] types.go (Module, Variable, Output, ProviderRequirement, ...)
- [x] registry/registry.go (interface + VersionsRequest)
- [x] registry/terraform.go (HashiCorp client)
- [x] registry/opentofu.go (OpenTofu client)
- [x] registry httptest-based unit tests (passing)

### 3. Versions
- [x] versions.go: GetLatestVersionMatch + resolveVersion (with tests)

### 4. Download + cache
- [x] download.go (go-getter/v2 wrapper, atomic rename)
- [x] cache.go (path layout, CacheStatus, functional options)
- [x] tests for cache helpers + go-getter (local & HTTP tar.gz)

### 5. Inspect + schema
- [ ] inspect.go (terraform-config-inspect → our Module)
- [ ] schema.go (Get* public methods)
- [ ] testdata/ fixture modules (root + submodules)

### 6. Server
- [ ] server.go (NewServer, options, orchestration)

### 7. CLI
- [ ] cmd/tfmoduleschema/main.go

### 8. Integration + release
- [ ] Integration tests (network, both registries)
- [ ] Example tests (godoc-runnable)
- [ ] Full README with usage
- [ ] .goreleaser.yaml
- [ ] release workflow

## Notes / decisions log

- 2026-04-20: Project scaffolded on worktree branch `initial`. Decisions captured in PLAN.md.
- Strategy: always download module source and parse with `terraform-config-inspect`
  (OpenTofu public registry does not expose rich metadata; download-and-parse is universal).
- JSON-only output, urfave/cli/v3, go-getter/v2, hashicorp/terraform-config-inspect, hashicorp/go-version.
