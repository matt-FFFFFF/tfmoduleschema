package tfmoduleschema

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectDir_Basic(t *testing.T) {
	t.Parallel()
	m, err := inspectDir(filepath.Join("testdata", "basic"), "")
	require.NoError(t, err)

	// Variables (sorted by name).
	require.Len(t, m.Variables, 4)
	names := []string{m.Variables[0].Name, m.Variables[1].Name, m.Variables[2].Name, m.Variables[3].Name}
	assert.Equal(t, []string{"location", "name", "secret", "tags"}, names)
	assert.True(t, m.Variables[2].Sensitive, "secret should be sensitive")
	assert.True(t, m.Variables[1].Required, "name should be required")
	assert.False(t, m.Variables[0].Required, "location has a default")
	assert.Equal(t, "westeurope", m.Variables[0].Default)

	// Outputs.
	require.Len(t, m.Outputs, 2)
	assert.Equal(t, "resource_group_id", m.Outputs[0].Name)
	assert.Equal(t, "tenant_id", m.Outputs[1].Name)
	assert.True(t, m.Outputs[1].Sensitive)

	// Required providers.
	require.Contains(t, m.RequiredProviders, "azurerm")
	require.Contains(t, m.RequiredProviders, "random")
	assert.Equal(t, "hashicorp/azurerm", m.RequiredProviders["azurerm"].Source)
	assert.Equal(t, []string{"~> 3.0"}, m.RequiredProviders["azurerm"].VersionConstraints)

	// Required core.
	assert.Equal(t, []string{">= 1.0.0"}, m.RequiredCore)

	// Resources.
	require.Len(t, m.ManagedResources, 1)
	assert.Equal(t, "azurerm_resource_group", m.ManagedResources[0].Type)
	assert.Equal(t, "managed", m.ManagedResources[0].Mode)
	require.Len(t, m.DataResources, 1)
	assert.Equal(t, "azurerm_client_config", m.DataResources[0].Type)
	assert.Equal(t, "data", m.DataResources[0].Mode)

	// Module calls.
	require.Contains(t, m.ModuleCalls, "network")
	assert.Equal(t, "./modules/network", m.ModuleCalls["network"].Source)
}

func TestListSubmoduleDirs(t *testing.T) {
	t.Parallel()
	subs, err := listSubmoduleDirs(filepath.Join("testdata", "basic"))
	require.NoError(t, err)
	assert.Equal(t, []string{"modules/network"}, subs)
}

func TestListSubmoduleDirs_NoModulesDir(t *testing.T) {
	t.Parallel()
	subs, err := listSubmoduleDirs(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, subs)
}

func TestInspectDir_Submodule(t *testing.T) {
	t.Parallel()
	m, err := inspectDir(filepath.Join("testdata", "basic", "modules", "network"), "modules/network")
	require.NoError(t, err)
	assert.Equal(t, "modules/network", m.Path)
	require.Len(t, m.Variables, 1)
	assert.Equal(t, "name", m.Variables[0].Name)
	require.Len(t, m.Outputs, 1)
	assert.Equal(t, "vnet_id", m.Outputs[0].Name)
}

func TestResolveSubmodulePath(t *testing.T) {
	t.Parallel()
	root := "/tmp/root"
	ok := []struct{ in, want string }{
		{"", root},
		{".", root},
		{"modules/network", filepath.Join(root, "modules/network")},
		{"modules/network/", filepath.Join(root, "modules/network")},
	}
	for _, tc := range ok {
		got, err := resolveSubmodulePath(root, tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}

	for _, bad := range []string{"../escape", "/abs", "modules/../../escape"} {
		_, err := resolveSubmodulePath(root, bad)
		assert.Error(t, err, bad)
	}
}
