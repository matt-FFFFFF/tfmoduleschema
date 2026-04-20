package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	goversion "github.com/hashicorp/go-version"
)

// versionsResponse is the JSON shape returned by the minimal module
// registry protocol's /versions endpoint. Both OpenTofu and HashiCorp
// return this shape.
type versionsResponse struct {
	Modules []struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	} `json:"modules"`
}

// downloadResponse is the JSON body shape returned by the /download
// endpoint when the registry chooses to use a body rather than the
// X-Terraform-Get header. OpenTofu typically returns this; HashiCorp
// typically returns 204 with X-Terraform-Get.
type downloadResponse struct {
	Location string `json:"location"`
}

// doGet issues a GET request and returns the response. Callers must close
// the body.
func doGet(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return client.Do(req)
}

// listVersions implements the shared /versions call used by both
// registries.
func listVersions(ctx context.Context, client *http.Client, baseURL string, req VersionsRequest) (goversion.Collection, error) {
	u := fmt.Sprintf("%s/%s/%s/%s/versions",
		strings.TrimRight(baseURL, "/"),
		url.PathEscape(req.Namespace),
		url.PathEscape(req.Name),
		url.PathEscape(req.System),
	)
	resp, err := doGet(ctx, client, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s/%s/%s", ErrModuleNotFound, req.Namespace, req.Name, req.System)
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("%w: GET %s: %s: %s", ErrRegistryAPI, u, resp.Status, strings.TrimSpace(string(body)))
	}

	var vr versionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return nil, fmt.Errorf("decoding versions response: %w", err)
	}
	if len(vr.Modules) == 0 {
		return nil, fmt.Errorf("%w: empty modules array", ErrRegistryAPI)
	}

	out := make(goversion.Collection, 0, len(vr.Modules[0].Versions))
	for _, v := range vr.Modules[0].Versions {
		ver, err := goversion.NewVersion(v.Version)
		if err != nil {
			// skip unparseable versions rather than failing the whole call
			continue
		}
		out = append(out, ver)
	}
	return out, nil
}

// resolveDownload implements the shared /download call. Both registries
// use the same endpoint shape. Some registries (HashiCorp) return 204 with
// X-Terraform-Get; others (OpenTofu) return 200 with a JSON body. We
// handle both and, when both are present, prefer the body (as OpenTofu
// documents).
//
// preferHeader selects header-first vs body-first lookup for the common
// case when a registry uses only one of the two.
func resolveDownload(ctx context.Context, client *http.Client, baseURL string, req DownloadRequest, preferHeader bool) (string, error) {
	endpoint := fmt.Sprintf("%s/%s/%s/%s/%s/download",
		strings.TrimRight(baseURL, "/"),
		url.PathEscape(req.Namespace),
		url.PathEscape(req.Name),
		url.PathEscape(req.System),
		url.PathEscape(req.Version),
	)
	resp, err := doGet(ctx, client, endpoint)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
	case http.StatusNotFound:
		return "", fmt.Errorf("%w: %s/%s/%s@%s", ErrModuleNotFound, req.Namespace, req.Name, req.System, req.Version)
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("%w: GET %s: %s: %s", ErrRegistryAPI, endpoint, resp.Status, strings.TrimSpace(string(body)))
	}

	var header, body string
	header = strings.TrimSpace(resp.Header.Get("X-Terraform-Get"))

	// Only attempt to decode JSON when there is content.
	if resp.StatusCode == http.StatusOK && resp.ContentLength != 0 {
		buf, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if err == nil && len(buf) > 0 {
			var dr downloadResponse
			if jerr := json.Unmarshal(buf, &dr); jerr == nil {
				body = strings.TrimSpace(dr.Location)
			}
		}
	}

	var loc string
	switch {
	case preferHeader && header != "":
		loc = header
	case !preferHeader && body != "":
		loc = body
	case header != "":
		loc = header
	case body != "":
		loc = body
	default:
		return "", fmt.Errorf("%w: GET %s: no download location (neither X-Terraform-Get header nor body.location)", ErrRegistryAPI, endpoint)
	}

	// If location is a relative URL, resolve it against the endpoint.
	if strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "./") || strings.HasPrefix(loc, "../") {
		base, err := url.Parse(endpoint)
		if err != nil {
			return "", fmt.Errorf("parsing endpoint URL: %w", err)
		}
		rel, err := url.Parse(loc)
		if err != nil {
			return "", fmt.Errorf("parsing relative location %q: %w", loc, err)
		}
		loc = base.ResolveReference(rel).String()
	}

	return loc, nil
}
