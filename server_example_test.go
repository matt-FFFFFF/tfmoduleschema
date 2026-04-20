package tfmoduleschema_test

import (
	"context"
	"fmt"
	"log"

	"github.com/matt-FFFFFF/tfmoduleschema"
)

// ExampleNewServer demonstrates fetching a module's variables and outputs
// from the OpenTofu registry (the default).
func ExampleNewServer() {
	s := tfmoduleschema.NewServer(nil)
	defer s.Cleanup() //nolint:errcheck

	req := tfmoduleschema.Request{
		Namespace: "terraform-aws-modules",
		Name:      "vpc",
		System:    "aws",
		Version:   "5.13.0",
	}

	m, err := s.GetModule(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("root path: %q\n", m.Path)
	fmt.Printf("variables: %d\n", len(m.Variables))
	fmt.Printf("outputs:   %d\n", len(m.Outputs))
}

// ExampleNewServer_terraformRegistry demonstrates using the HashiCorp
// Terraform registry by setting RegistryType on the Request.
func ExampleNewServer_terraformRegistry() {
	s := tfmoduleschema.NewServer(nil)
	defer s.Cleanup() //nolint:errcheck

	req := tfmoduleschema.Request{
		Namespace:    "terraform-aws-modules",
		Name:         "vpc",
		System:       "aws",
		Version:      "5.13.0",
		RegistryType: tfmoduleschema.RegistryTypeTerraform,
	}

	vars, err := s.GetVariables(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("variables: %d\n", len(vars))
}

// ExampleServer_GetAvailableVersions shows how to list every published
// version for a module.
func ExampleServer_GetAvailableVersions() {
	s := tfmoduleschema.NewServer(nil)
	defer s.Cleanup() //nolint:errcheck

	versions, err := s.GetAvailableVersions(context.Background(), tfmoduleschema.VersionsRequest{
		Namespace: "terraform-aws-modules",
		Name:      "vpc",
		System:    "aws",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("got %d versions; newest=%s\n", len(versions), versions[len(versions)-1])
}

// ExampleServer_ListSubmodules enumerates the first-level submodules
// shipped under "modules/" in the fetched module.
func ExampleServer_ListSubmodules() {
	s := tfmoduleschema.NewServer(nil)
	defer s.Cleanup() //nolint:errcheck

	req := tfmoduleschema.Request{
		Namespace: "terraform-aws-modules",
		Name:      "vpc",
		System:    "aws",
		Version:   "5.13.0",
	}

	subs, err := s.ListSubmodules(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range subs {
		fmt.Println(p)
	}
}

// ExampleServer_GetSubmodule fetches a single submodule by path
// relative to the module root.
func ExampleServer_GetSubmodule() {
	s := tfmoduleschema.NewServer(nil)
	defer s.Cleanup() //nolint:errcheck

	req := tfmoduleschema.Request{
		Namespace: "terraform-aws-modules",
		Name:      "vpc",
		System:    "aws",
		Version:   "5.13.0",
	}

	sub, err := s.GetSubmodule(context.Background(), req, "modules/vpc-endpoints")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("submodule %q: variables=%d outputs=%d\n", sub.Path, len(sub.Variables), len(sub.Outputs))
}

// ExampleWithCacheStatusFunc demonstrates observing cache hits and
// misses via a callback.
func ExampleWithCacheStatusFunc() {
	s := tfmoduleschema.NewServer(nil,
		tfmoduleschema.WithCacheStatusFunc(func(req tfmoduleschema.Request, status tfmoduleschema.CacheStatus) {
			fmt.Printf("%s: %s/%s/%s@%s\n", status, req.Namespace, req.Name, req.System, req.Version)
		}),
	)
	defer s.Cleanup() //nolint:errcheck

	_, _ = s.GetModule(context.Background(), tfmoduleschema.Request{
		Namespace: "terraform-aws-modules",
		Name:      "vpc",
		System:    "aws",
		Version:   "5.13.0",
	})
}
