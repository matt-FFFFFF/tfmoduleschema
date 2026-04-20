package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServer returns an httptest.Server with a handler implementing the
// minimal module registry protocol. Behaviour is controlled by the
// serverConfig: which style of /download response to return, and what
// versions/location to return.
type serverConfig struct {
	versions        []string
	downloadMode    string // "header", "body", "both", "relative-body", "none"
	location        string
	statusDownload  int // override; 0 = default per mode
	statusVersions  int // override; 0 = 200
	forceNotFoundOn string // path suffix that should 404
}

func newTestServer(t *testing.T, cfg serverConfig) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if cfg.forceNotFoundOn != "" && strings.HasSuffix(r.URL.Path, cfg.forceNotFoundOn) {
			http.NotFound(w, r)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions"):
			if cfg.statusVersions != 0 && cfg.statusVersions != http.StatusOK {
				w.WriteHeader(cfg.statusVersions)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			parts := make([]string, 0, len(cfg.versions))
			for _, v := range cfg.versions {
				parts = append(parts, fmt.Sprintf(`{"version":%q}`, v))
			}
			fmt.Fprintf(w, `{"modules":[{"versions":[%s]}]}`, strings.Join(parts, ","))
		case strings.HasSuffix(r.URL.Path, "/download"):
			switch cfg.downloadMode {
			case "header":
				w.Header().Set("X-Terraform-Get", cfg.location)
				if cfg.statusDownload != 0 {
					w.WriteHeader(cfg.statusDownload)
				} else {
					w.WriteHeader(http.StatusNoContent)
				}
			case "body":
				w.Header().Set("Content-Type", "application/json")
				if cfg.statusDownload != 0 {
					w.WriteHeader(cfg.statusDownload)
				}
				fmt.Fprintf(w, `{"location":%q}`, cfg.location)
			case "both":
				w.Header().Set("X-Terraform-Get", "header-"+cfg.location)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"location":%q}`, "body-"+cfg.location)
			case "relative-body":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"location":%q}`, cfg.location)
			case "none":
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unknown downloadMode %q", cfg.downloadMode)
			}
		default:
			http.NotFound(w, r)
		}
	})
	return httptest.NewServer(mux)
}

func TestListVersions(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverConfig{versions: []string{"1.0.0", "1.1.0", "2.0.0-beta", "garbage"}})
	defer srv.Close()

	for _, name := range []string{"opentofu", "terraform"} {
		t.Run(name, func(t *testing.T) {
			var reg Registry
			if name == "opentofu" {
				reg = NewOpenTofu(WithBaseURL(srv.URL))
			} else {
				reg = NewTerraform(WithBaseURL(srv.URL))
			}
			got, err := reg.ListVersions(context.Background(), VersionsRequest{Namespace: "ns", Name: "n", System: "s"})
			require.NoError(t, err)
			require.Len(t, got, 3, "garbage entry should be skipped")
			assert.Equal(t, "1.0.0", got[0].Original())
			assert.Equal(t, "1.1.0", got[1].Original())
			assert.Equal(t, "2.0.0-beta", got[2].Original())
		})
	}
}

func TestListVersions_NotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverConfig{forceNotFoundOn: "/versions"})
	defer srv.Close()

	reg := NewOpenTofu(WithBaseURL(srv.URL))
	_, err := reg.ListVersions(context.Background(), VersionsRequest{Namespace: "ns", Name: "n", System: "s"})
	require.ErrorIs(t, err, ErrModuleNotFound)
}

func TestResolveDownload_Header(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverConfig{downloadMode: "header", location: "git::https://example.com/foo.git?ref=v1"})
	defer srv.Close()

	reg := NewTerraform(WithBaseURL(srv.URL))
	loc, err := reg.ResolveDownload(context.Background(), DownloadRequest{Namespace: "ns", Name: "n", System: "s", Version: "1.0.0"})
	require.NoError(t, err)
	assert.Equal(t, "git::https://example.com/foo.git?ref=v1", loc)
}

func TestResolveDownload_Body(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverConfig{downloadMode: "body", location: "git::https://example.com/foo.git?ref=v2"})
	defer srv.Close()

	reg := NewOpenTofu(WithBaseURL(srv.URL))
	loc, err := reg.ResolveDownload(context.Background(), DownloadRequest{Namespace: "ns", Name: "n", System: "s", Version: "2.0.0"})
	require.NoError(t, err)
	assert.Equal(t, "git::https://example.com/foo.git?ref=v2", loc)
}

func TestResolveDownload_BothPreferences(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverConfig{downloadMode: "both", location: "X"})
	defer srv.Close()

	// Terraform prefers header
	tf := NewTerraform(WithBaseURL(srv.URL))
	loc, err := tf.ResolveDownload(context.Background(), DownloadRequest{Namespace: "ns", Name: "n", System: "s", Version: "1"})
	require.NoError(t, err)
	assert.Equal(t, "header-X", loc)

	// OpenTofu prefers body
	ot := NewOpenTofu(WithBaseURL(srv.URL))
	loc, err = ot.ResolveDownload(context.Background(), DownloadRequest{Namespace: "ns", Name: "n", System: "s", Version: "1"})
	require.NoError(t, err)
	assert.Equal(t, "body-X", loc)
}

func TestResolveDownload_Relative(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverConfig{downloadMode: "relative-body", location: "/archive/foo-1.0.0.tar.gz"})
	defer srv.Close()

	reg := NewOpenTofu(WithBaseURL(srv.URL))
	loc, err := reg.ResolveDownload(context.Background(), DownloadRequest{Namespace: "ns", Name: "n", System: "s", Version: "1.0.0"})
	require.NoError(t, err)
	assert.Equal(t, srv.URL+"/archive/foo-1.0.0.tar.gz", loc)
}

func TestResolveDownload_NoLocation(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverConfig{downloadMode: "none"})
	defer srv.Close()

	reg := NewOpenTofu(WithBaseURL(srv.URL))
	_, err := reg.ResolveDownload(context.Background(), DownloadRequest{Namespace: "ns", Name: "n", System: "s", Version: "1.0.0"})
	require.ErrorIs(t, err, ErrRegistryAPI)
}

func TestResolveDownload_NotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverConfig{forceNotFoundOn: "/download"})
	defer srv.Close()

	reg := NewTerraform(WithBaseURL(srv.URL))
	_, err := reg.ResolveDownload(context.Background(), DownloadRequest{Namespace: "ns", Name: "n", System: "s", Version: "1.0.0"})
	require.ErrorIs(t, err, ErrModuleNotFound)
}

func TestBaseURLDefaults(t *testing.T) {
	t.Parallel()
	assert.Equal(t, DefaultOpenTofuBaseURL, NewOpenTofu().BaseURL())
	assert.Equal(t, DefaultTerraformBaseURL, NewTerraform().BaseURL())
}
