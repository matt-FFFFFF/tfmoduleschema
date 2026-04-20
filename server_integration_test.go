package tfmoduleschema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests hit the live OpenTofu and HashiCorp Terraform module
// registries. They are kept minimal and pin concrete published versions
// for stability. Skipped automatically when `-short` is provided.

var integrationRegistries = []struct {
	name         string
	registryType RegistryType
}{
	{"OpenTofu", RegistryTypeOpenTofu},
	{"Terraform", RegistryTypeTerraform},
}

func TestServer_GetAvailableVersions_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	for _, reg := range integrationRegistries {
		t.Run(reg.name, func(t *testing.T) {
			s := NewServer(nil)
			defer func() { _ = s.Cleanup() }()

			versions, err := s.GetAvailableVersions(context.Background(), VersionsRequest{
				Namespace:    "terraform-aws-modules",
				Name:         "vpc",
				System:       "aws",
				RegistryType: reg.registryType,
			})
			require.NoError(t, err)
			require.Greater(t, len(versions), 0, "should list at least one version")
			t.Logf("got %d versions", len(versions))
		})
	}
}

func TestServer_GetModule_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	for _, reg := range integrationRegistries {
		t.Run(reg.name, func(t *testing.T) {
			s := NewServer(nil, WithCacheDir(t.TempDir()))
			defer func() { _ = s.Cleanup() }()

			req := Request{
				Namespace:    "terraform-aws-modules",
				Name:         "vpc",
				System:       "aws",
				Version:      "5.13.0",
				RegistryType: reg.registryType,
			}

			m, err := s.GetModule(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, m)
			assert.Equal(t, "", m.Path, "root module Path should be empty")
			assert.Greater(t, len(m.Variables), 0, "should have variables")
			assert.Greater(t, len(m.Outputs), 0, "should have outputs")
			assert.NotNil(t, m.RequiredProviders)

			varNames := make(map[string]bool, len(m.Variables))
			for _, v := range m.Variables {
				varNames[v.Name] = true
			}
			assert.True(t, varNames["name"] || varNames["cidr"], "expected typical VPC variable")

			t.Logf("variables=%d outputs=%d providers=%d", len(m.Variables), len(m.Outputs), len(m.RequiredProviders))
		})
	}
}

func TestServer_ListSubmodules_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	for _, reg := range integrationRegistries {
		t.Run(reg.name, func(t *testing.T) {
			s := NewServer(nil, WithCacheDir(t.TempDir()))
			defer func() { _ = s.Cleanup() }()

			req := Request{
				Namespace:    "terraform-aws-modules",
				Name:         "vpc",
				System:       "aws",
				Version:      "5.13.0",
				RegistryType: reg.registryType,
			}

			subs, err := s.ListSubmodules(context.Background(), req)
			require.NoError(t, err)
			t.Logf("submodules: %v", subs)
			// vpc module ships with submodules under modules/
			assert.Greater(t, len(subs), 0, "vpc module should have submodules")
		})
	}
}

func TestServer_LatestVersion_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	for _, reg := range integrationRegistries {
		t.Run(reg.name, func(t *testing.T) {
			s := NewServer(nil, WithCacheDir(t.TempDir()))
			defer func() { _ = s.Cleanup() }()

			// Empty Version requests the latest.
			req := Request{
				Namespace:    "terraform-aws-modules",
				Name:         "vpc",
				System:       "aws",
				RegistryType: reg.registryType,
			}
			m, err := s.GetModule(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, m)
			assert.Greater(t, len(m.Variables), 0)
		})
	}
}
