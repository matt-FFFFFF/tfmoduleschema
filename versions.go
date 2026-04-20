package tfmoduleschema

import (
	"context"
	"errors"
	"fmt"
	"sort"

	goversion "github.com/hashicorp/go-version"

	"github.com/matt-FFFFFF/tfmoduleschema/registry"
)

// ErrNoMatchingVersion is returned by GetLatestVersionMatch when the
// supplied constraints match none of the supplied versions.
var ErrNoMatchingVersion = errors.New("no matching version")

// GetLatestVersionMatch returns the highest version in versions that
// satisfies all of the given constraints. It returns ErrNoMatchingVersion
// if none do.
func GetLatestVersionMatch(versions goversion.Collection, constraints goversion.Constraints) (*goversion.Version, error) {
	if len(versions) == 0 {
		return nil, ErrNoMatchingVersion
	}
	sorted := make(goversion.Collection, len(versions))
	copy(sorted, versions)
	sort.Sort(sorted)

	for i := len(sorted) - 1; i >= 0; i-- {
		v := sorted[i]
		if constraints.Check(v) {
			return v, nil
		}
	}
	return nil, ErrNoMatchingVersion
}

// resolveVersion turns a possibly-empty or constraint-shaped version
// string into a concrete version. If version is empty, the latest
// available version is returned. If it parses as a concrete version it is
// returned as-is. Otherwise it is treated as a constraint expression and
// the registry is queried.
func resolveVersion(ctx context.Context, reg registry.Registry, vr registry.VersionsRequest, version string) (string, error) {
	if version != "" {
		if v, err := goversion.NewVersion(version); err == nil {
			return v.Original(), nil
		}
	}

	available, err := reg.ListVersions(ctx, vr)
	if err != nil {
		return "", err
	}
	if len(available) == 0 {
		return "", fmt.Errorf("%w: %s/%s/%s has no versions", ErrNoMatchingVersion, vr.Namespace, vr.Name, vr.System)
	}

	if version == "" {
		sorted := make(goversion.Collection, len(available))
		copy(sorted, available)
		sort.Sort(sorted)
		return sorted[len(sorted)-1].Original(), nil
	}

	constraints, err := goversion.NewConstraint(version)
	if err != nil {
		return "", fmt.Errorf("parsing version constraint %q: %w", version, err)
	}
	match, err := GetLatestVersionMatch(available, constraints)
	if err != nil {
		return "", fmt.Errorf("%w: constraint %q against %s/%s/%s", err, version, vr.Namespace, vr.Name, vr.System)
	}
	return match.Original(), nil
}
