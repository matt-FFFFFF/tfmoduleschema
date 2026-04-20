// Command tfmoduleschema is a CLI for inspecting Terraform modules from
// either the OpenTofu or the HashiCorp Terraform module registry.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	cli "github.com/urfave/cli/v3"

	"github.com/matt-FFFFFF/tfmoduleschema"
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
				Name:     "namespace",
				Aliases:  []string{"ns"},
				Usage:    "Module namespace (e.g. Azure, terraform-aws-modules)",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "name",
				Aliases:  []string{"n"},
				Usage:    "Module name (e.g. avm-res-compute-virtualmachine, vpc)",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "system",
				Aliases:  []string{"s"},
				Usage:    "Module target system (e.g. aws, azurerm). Called \"provider\" in the Hashi API",
				Required: true,
			},
			&cli.StringFlag{
				Name:    "version-constraint",
				Aliases: []string{"vc"},
				Usage:   "Version or constraint (e.g. 1.2.3, ~> 1.2). Empty for latest",
			},
			&cli.StringFlag{
				Name:    "registry",
				Aliases: []string{"r"},
				Usage:   "Registry type: opentofu (default) or terraform",
				Value:   "opentofu",
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

// requestFromCmd builds a Request from the global CLI flags.
func requestFromCmd(cmd *cli.Command) tfmoduleschema.Request {
	return tfmoduleschema.Request{
		Namespace:    cmd.String("namespace"),
		Name:         cmd.String("name"),
		System:       cmd.String("system"),
		Version:      cmd.String("version-constraint"),
		RegistryType: registryTypeFromString(cmd.String("registry")),
	}
}

func versionsRequestFromCmd(cmd *cli.Command) tfmoduleschema.VersionsRequest {
	return tfmoduleschema.VersionsRequest{
		Namespace:    cmd.String("namespace"),
		Name:         cmd.String("name"),
		System:       cmd.String("system"),
		RegistryType: registryTypeFromString(cmd.String("registry")),
	}
}

func registryTypeFromString(s string) tfmoduleschema.RegistryType {
	switch strings.ToLower(s) {
	case "terraform":
		return tfmoduleschema.RegistryTypeTerraform
	default:
		return tfmoduleschema.RegistryTypeOpenTofu
	}
}

func newServer(cmd *cli.Command) *tfmoduleschema.Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	opts := []tfmoduleschema.ServerOption{
		tfmoduleschema.WithCacheDir(cmd.String("cache-dir")),
		tfmoduleschema.WithForceFetch(cmd.Bool("force-fetch")),
	}
	if !cmd.Bool("quiet") {
		opts = append(opts, tfmoduleschema.WithCacheStatusFunc(func(req tfmoduleschema.Request, status tfmoduleschema.CacheStatus) {
			switch status {
			case tfmoduleschema.CacheStatusHit:
				fmt.Fprintf(os.Stderr, "cache hit: %s/%s/%s %s\n", req.Namespace, req.Name, req.System, req.Version)
			case tfmoduleschema.CacheStatusMiss:
				fmt.Fprintf(os.Stderr, "downloading: %s/%s/%s %s\n", req.Namespace, req.Name, req.System, req.Version)
			}
		}))
	}
	return tfmoduleschema.NewServer(logger, opts...)
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
		Usage: "Inspect the root module",
		Commands: []*cli.Command{{
			Name:  "schema",
			Usage: "Print the full parsed root module as JSON",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				s := newServer(cmd)
				defer s.Cleanup()
				m, err := s.GetModule(ctx, requestFromCmd(cmd))
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
					s := newServer(cmd)
					defer s.Cleanup()
					vars, err := s.GetVariables(ctx, requestFromCmd(cmd))
					if err != nil {
						return err
					}
					names := make([]string, 0, len(vars))
					for _, v := range vars {
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
					s := newServer(cmd)
					defer s.Cleanup()
					vars, err := s.GetVariables(ctx, requestFromCmd(cmd))
					if err != nil {
						return err
					}
					name := cmd.Args().First()
					if name == "" {
						return printJSON(vars)
					}
					for _, v := range vars {
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
					s := newServer(cmd)
					defer s.Cleanup()
					outs, err := s.GetOutputs(ctx, requestFromCmd(cmd))
					if err != nil {
						return err
					}
					names := make([]string, 0, len(outs))
					for _, o := range outs {
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
					s := newServer(cmd)
					defer s.Cleanup()
					outs, err := s.GetOutputs(ctx, requestFromCmd(cmd))
					if err != nil {
						return err
					}
					name := cmd.Args().First()
					if name == "" {
						return printJSON(outs)
					}
					for _, o := range outs {
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
					s := newServer(cmd)
					defer s.Cleanup()
					reqs, err := s.GetProviderRequirements(ctx, requestFromCmd(cmd))
					if err != nil {
						return err
					}
					names := make([]string, 0, len(reqs))
					for n := range reqs {
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
					s := newServer(cmd)
					defer s.Cleanup()
					reqs, err := s.GetProviderRequirements(ctx, requestFromCmd(cmd))
					if err != nil {
						return err
					}
					name := cmd.Args().First()
					if name == "" {
						return printJSON(reqs)
					}
					pr, ok := reqs[name]
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
					s := newServer(cmd)
					defer s.Cleanup()
					subs, err := s.ListSubmodules(ctx, requestFromCmd(cmd))
					if err != nil {
						return err
					}
					printList(subs)
					return nil
				},
			},
			{
				Name:      "schema",
				Usage:     "Print full schema for one submodule by path (e.g. modules/network)",
				ArgsUsage: "<submodule-path>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					args := cmd.Args()
					if args.Len() < 1 {
						return fmt.Errorf("submodule path is required (e.g. modules/network)")
					}
					s := newServer(cmd)
					defer s.Cleanup()
					m, err := s.GetSubmodule(ctx, requestFromCmd(cmd), args.First())
					if err != nil {
						return err
					}
					return printJSON(m)
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
				s := newServer(cmd)
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
