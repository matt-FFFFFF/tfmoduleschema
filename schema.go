package tfmoduleschema

import (
	"context"
	"fmt"

	goversion "github.com/hashicorp/go-version"

	"github.com/matt-FFFFFF/tfmoduleschema/registry"
)

// GetAvailableVersions returns all versions the registry advertises for
// the module identified by req.
func (s *Server) GetAvailableVersions(ctx context.Context, req VersionsRequest) (goversion.Collection, error) {
	// normalise by running through a Request just to reuse validation
	if _, err := s.normalise(Request{
		Namespace:    req.Namespace,
		Name:         req.Name,
		System:       req.System,
		RegistryType: req.RegistryType,
	}); err != nil {
		return nil, err
	}
	reg := s.registryFor(req.RegistryType)
	return reg.ListVersions(ctx, registry.VersionsRequest{
		Namespace: req.Namespace, Name: req.Name, System: req.System,
	})
}

// GetModule returns the parsed root module for the given request. If
// req.Version is empty or a constraint, the latest satisfying version is
// resolved against the registry first.
func (s *Server) GetModule(ctx context.Context, req Request) (*Module, error) {
	return s.getModule(ctx, req, "")
}

// GetSubmodule returns the parsed submodule at the given path (relative
// to the module root, e.g. "modules/network").
func (s *Server) GetSubmodule(ctx context.Context, req Request, subpath string) (*Module, error) {
	return s.getModule(ctx, req, subpath)
}

// ListSubmodules returns the paths of first-level submodules under
// modules/ within the fetched module.
func (s *Server) ListSubmodules(ctx context.Context, req Request) ([]string, error) {
	req, err := s.resolveRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	dir, err := s.fetchModule(ctx, req)
	if err != nil {
		return nil, err
	}
	return listSubmoduleDirs(dir)
}

// GetVariables is a convenience accessor returning just the root
// module's variables.
func (s *Server) GetVariables(ctx context.Context, req Request) ([]Variable, error) {
	m, err := s.GetModule(ctx, req)
	if err != nil {
		return nil, err
	}
	return m.Variables, nil
}

// GetOutputs is a convenience accessor returning just the root module's
// outputs.
func (s *Server) GetOutputs(ctx context.Context, req Request) ([]Output, error) {
	m, err := s.GetModule(ctx, req)
	if err != nil {
		return nil, err
	}
	return m.Outputs, nil
}

// GetProviderRequirements returns the root module's required_providers
// map.
func (s *Server) GetProviderRequirements(ctx context.Context, req Request) (map[string]ProviderRequirement, error) {
	m, err := s.GetModule(ctx, req)
	if err != nil {
		return nil, err
	}
	return m.RequiredProviders, nil
}

// getModule is the shared implementation for root/submodule retrieval.
// subpath == "" means the root module.
func (s *Server) getModule(ctx context.Context, req Request, subpath string) (*Module, error) {
	req, err := s.resolveRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	key := moduleKey{
		registry: req.RegistryType,
		ns:       req.Namespace, name: req.Name, system: req.System,
		version: req.Version, subpath: subpath,
	}

	s.mu.RLock()
	if m, ok := s.moduleCache[key]; ok {
		s.mu.RUnlock()
		return m, nil
	}
	s.mu.RUnlock()

	dir, err := s.fetchModule(ctx, req)
	if err != nil {
		return nil, err
	}

	target, err := resolveSubmodulePath(dir, subpath)
	if err != nil {
		return nil, err
	}

	m, err := inspectDir(target, subpath)
	if err != nil {
		return nil, fmt.Errorf("inspecting module %q: %w", target, err)
	}

	s.mu.Lock()
	s.moduleCache[key] = m
	s.mu.Unlock()
	return m, nil
}

// resolveRequest validates req and resolves a concrete version, mutating
// a copy of req.
func (s *Server) resolveRequest(ctx context.Context, req Request) (Request, error) {
	req, err := s.normalise(req)
	if err != nil {
		return req, err
	}
	reg := s.registryFor(req.RegistryType)
	resolved, err := resolveVersion(ctx, reg, registry.VersionsRequest{
		Namespace: req.Namespace, Name: req.Name, System: req.System,
	}, req.Version)
	if err != nil {
		return req, err
	}
	req.Version = resolved
	return req, nil
}
