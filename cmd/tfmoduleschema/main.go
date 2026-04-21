// Command tfmoduleschema is a CLI for inspecting Terraform modules from
// either the OpenTofu or the HashiCorp Terraform module registry.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path"
	"sort"
	"strings"

	goversion "github.com/hashicorp/go-version"
	cli "github.com/urfave/cli/v3"

	"github.com/matt-FFFFFF/tfmoduleschema"
	"github.com/matt-FFFFFF/tfmoduleschema/registry"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cmd := buildRootCommand()
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func buildRootCommand() *cli.Command {
	return &cli.Command{
		Name:    "tfmoduleschema",
		Usage:   "Query Terraform/OpenTofu module metadata from the registry",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"ns"},
				Usage:   "Module namespace (e.g. Azure, terraform-aws-modules). Required unless --source is set",
			},
			&cli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Usage:   "Module name (e.g. avm-res-compute-virtualmachine, vpc). Required unless --source is set",
			},
			&cli.StringFlag{
				Name:    "system",
				Aliases: []string{"s"},
				Usage:   "Module target system (e.g. aws, azurerm). Called \"provider\" in the Hashi API. Required unless --source is set",
			},
			&cli.StringFlag{
				Name:    "version-constraint",
				Aliases: []string{"vc"},
				Usage:   "Version or constraint (e.g. 1.2.3, ~> 1.2). Empty for latest. Must be a concrete version when --source is set",
			},
			&cli.StringFlag{
				Name:    "submodule",
				Aliases: []string{"sm"},
				Usage:   "Target a submodule by path (e.g. modules/network) instead of the root module",
			},
			&cli.StringFlag{
				Name:    "source",
				Usage:   "Local path or go-getter source (e.g. ./mymod, git::https://...). Mutually exclusive with --namespace/--name/--system",
			},
			&cli.StringFlag{
				Name:    "registry",
				Aliases: []string{"r"},
				Usage:   "Registry type: opentofu (default), terraform, or custom",
				Value:   "opentofu",
			},
			&cli.StringFlag{
				Name:  "registry-url",
				Usage: "Custom registry host or base URL (implies --registry=custom). Host-only input triggers Terraform remote service discovery",
			},
			&cli.StringFlag{
				Name:    "registry-token",
				Usage:   "Bearer token for the custom registry (overrides TF_TOKEN_<host> and credentials.tfrc.json)",
				Sources: cli.EnvVars("TFMODULESCHEMA_REGISTRY_TOKEN"),
			},
			&cli.StringFlag{
				Name:    "cache-dir",
				Usage:   "Directory used to cache downloaded modules (overrides $" + tfmoduleschema.EnvCacheDir + ")",
				Sources: cli.EnvVars(tfmoduleschema.EnvCacheDir),
			},
			&cli.BoolFlag{Name: "force-fetch", Usage: "Always download the module, bypassing the local cache"},
			&cli.BoolFlag{Name: "quiet", Usage: "Suppress cache hit/miss status messages on stderr"},
		},
		Commands: []*cli.Command{
			moduleCommand(),
			variableCommand(),
			outputCommand(),
			providerCommand(),
			submoduleCommand(),
			versionCommand(),
		},
	}
}

// requestFromCmd builds a Request from the global CLI flags. It does
// NOT validate flag combinations; call validateRequestFlags first.
func requestFromCmd(cmd *cli.Command) tfmoduleschema.Request {
	return tfmoduleschema.Request{
		Namespace:    cmd.String("namespace"),
		Name:         cmd.String("name"),
		System:       cmd.String("system"),
		Version:      cmd.String("version-constraint"),
		Source:       cmd.String("source"),
		RegistryType: registryTypeFromCmd(cmd),
	}
}

func versionsRequestFromCmd(cmd *cli.Command) tfmoduleschema.VersionsRequest {
	return tfmoduleschema.VersionsRequest{
		Namespace:    cmd.String("namespace"),
		Name:         cmd.String("name"),
		System:       cmd.String("system"),
		RegistryType: registryTypeFromCmd(cmd),
	}
}

// registryTypeFromCmd returns the effective registry type, honouring
// the implicit "--registry-url forces custom" rule.
func registryTypeFromCmd(cmd *cli.Command) tfmoduleschema.RegistryType {
	if strings.TrimSpace(cmd.String("registry-url")) != "" {
		return tfmoduleschema.RegistryTypeCustom
	}
	return registryTypeFromString(cmd.String("registry"))
}

func registryTypeFromString(s string) tfmoduleschema.RegistryType {
	switch strings.ToLower(s) {
	case "terraform":
		return tfmoduleschema.RegistryTypeTerraform
	case "custom":
		return tfmoduleschema.RegistryTypeCustom
	default:
		return tfmoduleschema.RegistryTypeOpenTofu
	}
}

// validateRequestFlags enforces flag-combination rules that would
// otherwise manifest as confusing downstream errors. It must be called
// before dispatching a Request or a VersionsRequest.
//
// Rules:
//   - --source is mutually exclusive with --namespace, --name, --system,
//     --registry-url, and --registry-token.
//   - When --source is not set, all three of --namespace/--name/--system
//     are required (mirroring the old Required:true behaviour, now
//     enforced in code so --source can opt out).
//   - When --registry=custom is selected (explicitly or via
//     --registry-url), --registry-url must be supplied unless --source
//     is set, and its value must be a syntactically valid
//     custom-registry input so that WithCustomRegistry does not panic.
func validateRequestFlags(cmd *cli.Command) error {
	source := strings.TrimSpace(cmd.String("source"))
	ns := strings.TrimSpace(cmd.String("namespace"))
	name := strings.TrimSpace(cmd.String("name"))
	sys := strings.TrimSpace(cmd.String("system"))
	regURL := strings.TrimSpace(cmd.String("registry-url"))
	regTok := strings.TrimSpace(cmd.String("registry-token"))
	version := strings.TrimSpace(cmd.String("version-constraint"))

	if source != "" {
		if ns != "" || name != "" || sys != "" {
			return fmt.Errorf("--source is mutually exclusive with --namespace/--name/--system")
		}
		if regURL != "" || regTok != "" {
			return fmt.Errorf("--source is mutually exclusive with --registry-url/--registry-token")
		}
		// Source mode has no registry to resolve a constraint
		// against, so --version-constraint must either be empty or
		// a concrete version (e.g. "1.2.3"), not a range like ">=
		// 1.0". Catching this here turns a confusing server-side
		// error into an immediate CLI validation failure.
		if version != "" {
			if _, err := goversion.NewVersion(version); err != nil {
				return fmt.Errorf("--source requires --version-constraint to be empty or a concrete version (got %q)", version)
			}
		}
		return nil
	}
	// No source: classic registry path.
	var missing []string
	if ns == "" {
		missing = append(missing, "--namespace")
	}
	if name == "" {
		missing = append(missing, "--name")
	}
	if sys == "" {
		missing = append(missing, "--system")
	}
	if len(missing) > 0 {
		return fmt.Errorf("required flag(s) missing: %s", strings.Join(missing, ", "))
	}
	// Classic registry path: --registry=custom must have a URL. This
	// catches `--registry custom` without `--registry-url`, which
	// would otherwise fail much later with "no custom registry
	// configured" from the server.
	if registryTypeFromCmd(cmd) == tfmoduleschema.RegistryTypeCustom && regURL == "" {
		return fmt.Errorf("--registry=custom requires --registry-url")
	}
	// Pre-validate --registry-url shape so newServer's call to
	// WithCustomRegistry (which panics on malformed input) never
	// trips on user-supplied strings. Callers reach newServer through
	// multiple dispatch paths; catching the error here keeps CLI
	// failures as normal non-zero-exit messages.
	if regURL != "" {
		if _, _, err := registry.DiscoverModulesEndpointInputCheck(regURL); err != nil {
			return fmt.Errorf("invalid --registry-url %q: %w", regURL, err)
		}
	}
	return nil
}

// submoduleFromCmd returns the cleaned submodule path from --submodule,
// or "" when the flag is unset. It rejects absolute paths and any path
// that escapes the module root via "..".
func submoduleFromCmd(cmd *cli.Command) (string, error) {
	raw := strings.TrimSpace(cmd.String("submodule"))
	if raw == "" {
		return "", nil
	}
	if strings.ContainsRune(raw, '\\') {
		return "", fmt.Errorf("invalid --submodule path %q: use forward slashes", raw)
	}
	if hasWindowsVolume(raw) {
		return "", fmt.Errorf("invalid --submodule path %q: must be relative to the module root", raw)
	}
	if path.IsAbs(raw) {
		return "", fmt.Errorf("invalid --submodule path %q: must be relative to the module root", raw)
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("invalid --submodule path %q: must not be empty or '.'", raw)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("invalid --submodule path %q: must not escape the module root", raw)
	}
	return cleaned, nil
}

// hasWindowsVolume reports whether p begins with a Windows drive-letter
// volume (e.g. "C:", "C:/foo"). path.IsAbs only recognises '/'-rooted
// paths, so we check explicitly so --submodule validation is consistent
// regardless of which OS the CLI is running on.
func hasWindowsVolume(p string) bool {
	if len(p) < 2 || p[1] != ':' {
		return false
	}
	c := p[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// loadModule fetches either the root module or the submodule designated
// by --submodule, depending on whether the flag is set.
func loadModule(ctx context.Context, s *tfmoduleschema.Server, cmd *cli.Command) (*tfmoduleschema.Module, error) {
	if err := validateRequestFlags(cmd); err != nil {
		return nil, err
	}
	sub, err := submoduleFromCmd(cmd)
	if err != nil {
		return nil, err
	}
	if sub == "" {
		return s.GetModule(ctx, requestFromCmd(cmd))
	}
	return s.GetSubmodule(ctx, requestFromCmd(cmd), sub)
}

func newServer(cmd *cli.Command) (*tfmoduleschema.Server, error) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	opts := []tfmoduleschema.ServerOption{
		tfmoduleschema.WithCacheDir(cmd.String("cache-dir")),
		tfmoduleschema.WithForceFetch(cmd.Bool("force-fetch")),
	}
	if url := strings.TrimSpace(cmd.String("registry-url")); url != "" {
		// Pre-validate so WithCustomRegistry (which panics on
		// malformed input) can't crash the CLI from user input. The
		// same check is also performed in validateRequestFlags for
		// early error reporting, but newServer is reached through
		// multiple paths; this is the final backstop.
		if _, _, err := registry.DiscoverModulesEndpointInputCheck(url); err != nil {
			return nil, fmt.Errorf("invalid --registry-url %q: %w", url, err)
		}
		var regOpts []registry.Option
		if tok := strings.TrimSpace(cmd.String("registry-token")); tok != "" {
			regOpts = append(regOpts, registry.WithBearerToken(tok))
		}
		opts = append(opts, tfmoduleschema.WithCustomRegistry(url, regOpts...))
	}
	if !cmd.Bool("quiet") {
		opts = append(opts, tfmoduleschema.WithCacheStatusFunc(func(req tfmoduleschema.Request, status tfmoduleschema.CacheStatus) {
			switch status {
			case tfmoduleschema.CacheStatusHit:
				fmt.Fprintf(os.Stderr, "cache hit: %s\n", cacheLabel(req))
			case tfmoduleschema.CacheStatusMiss:
				fmt.Fprintf(os.Stderr, "downloading: %s\n", cacheLabel(req))
			}
		}))
	}
	return tfmoduleschema.NewServer(logger, opts...), nil
}

// cacheLabel produces a human label for a Request, preferring Source
// when set so --source users don't see empty namespace/name fields.
func cacheLabel(req tfmoduleschema.Request) string {
	if req.Source != "" {
		return req.Source
	}
	return fmt.Sprintf("%s/%s/%s %s", req.Namespace, req.Name, req.System, req.Version)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printList(items []string) {
	for _, item := range items {
		fmt.Println(item)
	}
}

// --- module ---

func moduleCommand() *cli.Command {
	return &cli.Command{
		Name:  "module",
		Usage: "Inspect the root module (or a submodule via --submodule)",
		Commands: []*cli.Command{{
			Name:  "schema",
			Usage: "Print the full parsed module as JSON",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				s, err := newServer(cmd)
				if err != nil {
					return err
				}
				defer s.Cleanup()
				m, err := loadModule(ctx, s, cmd)
				if err != nil {
					return err
				}
				return printJSON(m)
			},
		}},
	}
}

// --- variable ---

func variableCommand() *cli.Command {
	return &cli.Command{
		Name:  "variable",
		Usage: "Query module input variables",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List variable names",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					s, err := newServer(cmd)
					if err != nil {
						return err
					}
					defer s.Cleanup()
					m, err := loadModule(ctx, s, cmd)
					if err != nil {
						return err
					}
					names := make([]string, 0, len(m.Variables))
					for _, v := range m.Variables {
						names = append(names, v.Name)
					}
					printList(names)
					return nil
				},
			},
			{
				Name:      "schema",
				Usage:     "Print full schema for one variable, or all when no name given",
				ArgsUsage: "[variable-name]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					s, err := newServer(cmd)
					if err != nil {
						return err
					}
					defer s.Cleanup()
					m, err := loadModule(ctx, s, cmd)
					if err != nil {
						return err
					}
					name := cmd.Args().First()
					if name == "" {
						return printJSON(m.Variables)
					}
					for _, v := range m.Variables {
						if v.Name == name {
							return printJSON(v)
						}
					}
					return fmt.Errorf("variable %q not found", name)
				},
			},
		},
	}
}

// --- output ---

func outputCommand() *cli.Command {
	return &cli.Command{
		Name:  "output",
		Usage: "Query module outputs",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List output names",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					s, err := newServer(cmd)
					if err != nil {
						return err
					}
					defer s.Cleanup()
					m, err := loadModule(ctx, s, cmd)
					if err != nil {
						return err
					}
					names := make([]string, 0, len(m.Outputs))
					for _, o := range m.Outputs {
						names = append(names, o.Name)
					}
					printList(names)
					return nil
				},
			},
			{
				Name:      "schema",
				Usage:     "Print full schema for one output, or all when no name given",
				ArgsUsage: "[output-name]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					s, err := newServer(cmd)
					if err != nil {
						return err
					}
					defer s.Cleanup()
					m, err := loadModule(ctx, s, cmd)
					if err != nil {
						return err
					}
					name := cmd.Args().First()
					if name == "" {
						return printJSON(m.Outputs)
					}
					for _, o := range m.Outputs {
						if o.Name == name {
							return printJSON(o)
						}
					}
					return fmt.Errorf("output %q not found", name)
				},
			},
		},
	}
}

// --- provider ---

func providerCommand() *cli.Command {
	return &cli.Command{
		Name:  "provider",
		Usage: "Query required_providers",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List required provider names",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					s, err := newServer(cmd)
					if err != nil {
						return err
					}
					defer s.Cleanup()
					m, err := loadModule(ctx, s, cmd)
					if err != nil {
						return err
					}
					names := make([]string, 0, len(m.RequiredProviders))
					for n := range m.RequiredProviders {
						names = append(names, n)
					}
					sort.Strings(names)
					printList(names)
					return nil
				},
			},
			{
				Name:      "schema",
				Usage:     "Print requirement for one provider, or the full map when no name given",
				ArgsUsage: "[provider-name]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					s, err := newServer(cmd)
					if err != nil {
						return err
					}
					defer s.Cleanup()
					m, err := loadModule(ctx, s, cmd)
					if err != nil {
						return err
					}
					name := cmd.Args().First()
					if name == "" {
						return printJSON(m.RequiredProviders)
					}
					pr, ok := m.RequiredProviders[name]
					if !ok {
						return fmt.Errorf("provider %q not found in required_providers", name)
					}
					return printJSON(pr)
				},
			},
		},
	}
}

// --- submodule ---

func submoduleCommand() *cli.Command {
	return &cli.Command{
		Name:  "submodule",
		Usage: "Query submodules under modules/",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List submodule paths",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := validateRequestFlags(cmd); err != nil {
					return err
				}
				s, err := newServer(cmd)
				if err != nil {
					return err
				}
				defer s.Cleanup()
				subs, err := s.ListSubmodules(ctx, requestFromCmd(cmd))
				if err != nil {
					return err
				}
				printList(subs)
				return nil
			},
			},
		},
	}
}

// --- version ---

func versionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Query available module versions",
		Commands: []*cli.Command{{
			Name:  "list",
			Usage: "List all versions the registry advertises",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if strings.TrimSpace(cmd.String("source")) != "" {
					return fmt.Errorf("version list is not supported with --source: pin a concrete version in the source URL instead")
				}
				if err := validateRequestFlags(cmd); err != nil {
					return err
				}
				s, err := newServer(cmd)
				if err != nil {
					return err
				}
				defer s.Cleanup()
				vs, err := s.GetAvailableVersions(ctx, versionsRequestFromCmd(cmd))
				if err != nil {
					return err
				}
				for _, v := range vs {
					fmt.Println(v.Original())
				}
				return nil
			},
		}},
	}
}
