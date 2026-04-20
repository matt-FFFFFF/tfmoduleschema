package tfmoduleschema

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	goversion "github.com/hashicorp/go-version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/matt-FFFFFF/tfmoduleschema/registry"
)

func TestGetLatestVersionMatch(t *testing.T) {
	t.Parallel()
	mk := func(ss ...string) goversion.Collection {
		out := make(goversion.Collection, 0, len(ss))
		for _, s := range ss {
			v, err := goversion.NewVersion(s)
			require.NoError(t, err)
			out = append(out, v)
		}
		return out
	}

	tests := []struct {
		name       string
		versions   goversion.Collection
		constraint string
		want       string
		wantErr    bool
	}{
		{"exact", mk("1.0.0", "1.2.3", "2.0.0"), "1.2.3", "1.2.3", false},
		{"caret-like", mk("1.0.0", "1.2.3", "1.3.0", "2.0.0"), "~> 1.2", "1.3.0", false},
		{"range", mk("1.0.0", "1.2.3", "2.0.0"), ">= 1.0, < 2.0", "1.2.3", false},
		{"no-match", mk("1.0.0", "1.2.3"), ">= 3.0", "", true},
		{"empty-versions", nil, ">= 1", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var cs goversion.Constraints
			if tc.constraint != "" {
				var err error
				cs, err = goversion.NewConstraint(tc.constraint)
				require.NoError(t, err)
			}
			got, err := GetLatestVersionMatch(tc.versions, cs)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Original())
		})
	}
}

func TestResolveVersion(t *testing.T) {
	t.Parallel()
	// local test server returning a fixed version set
	mux := http.NewServeMux()
	versions := []string{"1.0.0", "1.1.0", "1.2.0", "2.0.0-beta", "2.0.0"}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		parts := make([]string, 0, len(versions))
		for _, v := range versions {
			parts = append(parts, fmt.Sprintf(`{"version":%q}`, v))
		}
		fmt.Fprintf(w, `{"modules":[{"versions":[%s]}]}`, strings.Join(parts, ","))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := registry.NewOpenTofu(registry.WithBaseURL(srv.URL))
	vr := registry.VersionsRequest{Namespace: "ns", Name: "n", System: "s"}

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty-latest", "", "2.0.0", false},
		{"concrete-passthrough", "1.1.0", "1.1.0", false},
		{"concrete-even-if-missing", "9.9.9", "9.9.9", false}, // no registry call
		{"constraint", "~> 1.1", "1.2.0", false},
		{"constraint-nomatch", ">= 3.0", "", true},
		{"malformed-constraint", "not-a-version-or-constraint", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveVersion(context.Background(), reg, vr, tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
