package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrCredentialsFile wraps failures parsing a Terraform credentials
// file. Missing files are NOT treated as errors — ResolveTokenForHost
// simply returns no token in that case.
var ErrCredentialsFile = errors.New("credentials file")

// ResolveTokenForHost returns a bearer token for host, resolved in
// Terraform's documented order:
//
//  1. The TF_TOKEN_<host> environment variable. Dots in the hostname
//     are replaced with underscores and hyphens with double
//     underscores, per
//     https://developer.hashicorp.com/terraform/cli/config/config-file#environment-variable-credentials
//     Colons in host:port are replaced with underscores (the
//     environment-variable name must be a valid identifier).
//
//  2. The credentials block in the JSON credentials file
//     (credentials.tfrc.json). Only the JSON form is read; legacy HCL
//     .terraformrc credentials are not parsed.
//
// host may include a port ("registry.example.com:8443"); matching is
// case-insensitive. If host has a port, the exact host:port key is
// tried first; on miss, a bare-hostname fallback is attempted.
//
// An empty string is returned with a nil error when no token is
// available; callers should treat this as "send unauthenticated".
func ResolveTokenForHost(host string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return "", nil
	}

	// 1. Environment variable.
	if tok := lookupTokenEnv(h); tok != "" {
		return tok, nil
	}

	// 2. Credentials file.
	tok, err := lookupTokenFile(h)
	if err != nil {
		return "", err
	}
	return tok, nil
}

// lookupTokenEnv returns the value of TF_TOKEN_<host-as-identifier>,
// checking host:port first and bare-host second.
func lookupTokenEnv(host string) string {
	keys := candidateEnvKeys(host)
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			return v
		}
	}
	return ""
}

// candidateEnvKeys returns the TF_TOKEN_* environment variable names
// to consult for host, most specific first. The transformation rules
// are: dots -> "_", hyphens -> "__", colons -> "_". The prefix is
// always "TF_TOKEN_".
func candidateEnvKeys(host string) []string {
	primary := "TF_TOKEN_" + encodeHostForEnv(host)
	if bare, _, ok := splitHostPort(host); ok {
		return []string{primary, "TF_TOKEN_" + encodeHostForEnv(bare)}
	}
	return []string{primary}
}

// encodeHostForEnv applies the Terraform host->env-variable-suffix
// transformation. Callers must lowercase the host before calling.
func encodeHostForEnv(host string) string {
	// Hyphen must become "__" BEFORE dots become "_" — otherwise
	// "-" -> "__" would collide with dot encoding. Do it in one
	// pass with a strings.Builder to avoid intermediate allocs.
	var b strings.Builder
	b.Grow(len(host))
	for _, r := range host {
		switch r {
		case '-':
			b.WriteString("__")
		case '.':
			b.WriteByte('_')
		case ':':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitHostPort returns (host, port, true) when host contains a port,
// otherwise ("", "", false). Does not validate port digits — callers
// have already parsed the input upstream. Inputs with an empty host
// portion (e.g. ":8443") are rejected so they don't collapse onto
// the empty-host TF_TOKEN_ key.
func splitHostPort(host string) (string, string, bool) {
	i := strings.LastIndex(host, ":")
	if i <= 0 || i == len(host)-1 {
		return "", "", false
	}
	// Reject IPv6-literal style "[::1]:443" — our hosts are
	// registry DNS names, never bracketed literals. If we ever need
	// IPv6 support, revisit.
	if strings.Contains(host, "[") {
		return "", "", false
	}
	return host[:i], host[i+1:], true
}

// credentialsDoc is the shape of credentials.tfrc.json:
//
//	{"credentials": {"app.terraform.io": {"token": "..."}}}
type credentialsDoc struct {
	Credentials map[string]credentialsEntry `json:"credentials"`
}

type credentialsEntry struct {
	Token string `json:"token"`
}

// lookupTokenFile searches the documented credentials file locations
// and returns the first matching token, or "" if none. A parse error
// in a file that exists is surfaced; a missing file is not an error.
func lookupTokenFile(host string) (string, error) {
	for _, path := range credentialsFilePaths() {
		tok, found, err := readCredentialsFile(path, host)
		if err != nil {
			return "", err
		}
		if found {
			return tok, nil
		}
	}
	return "", nil
}

// credentialsFilePaths returns the ordered list of file paths to
// consult. Order matches Terraform's behaviour: TF_CLI_CONFIG_FILE
// wins; otherwise XDG config dir; otherwise the platform-default
// terraform.d directory.
func credentialsFilePaths() []string {
	var paths []string
	if p := os.Getenv("TF_CLI_CONFIG_FILE"); p != "" {
		paths = append(paths, p)
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "terraform", "credentials.tfrc.json"))
	}
	if runtime.GOOS == "windows" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			paths = append(paths, filepath.Join(appdata, "terraform.d", "credentials.tfrc.json"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".terraform.d", "credentials.tfrc.json"))
	}
	return paths
}

// readCredentialsFile loads path and returns (token, found, err).
// found is true only when the file parsed and contained a matching
// credentials entry for host. A non-existent file returns (,false,nil);
// only parse errors propagate.
func readCredentialsFile(path, host string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("%w: reading %s: %v", ErrCredentialsFile, path, err)
	}
	var doc credentialsDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", false, fmt.Errorf("%w: parsing %s: %v", ErrCredentialsFile, path, err)
	}
	if tok, ok := lookupCredentialsHost(doc.Credentials, host); ok {
		return tok, true, nil
	}
	return "", false, nil
}

// lookupCredentialsHost returns the token for host, trying the exact
// host first and then the bare hostname (for host:port inputs). Map
// keys are compared case-insensitively.
func lookupCredentialsHost(m map[string]credentialsEntry, host string) (string, bool) {
	if tok, ok := lookupExactCI(m, host); ok {
		return tok, true
	}
	if bare, _, ok := splitHostPort(host); ok {
		if tok, ok := lookupExactCI(m, bare); ok {
			return tok, true
		}
	}
	return "", false
}

func lookupExactCI(m map[string]credentialsEntry, key string) (string, bool) {
	for k, v := range m {
		if strings.EqualFold(k, key) && v.Token != "" {
			return v.Token, true
		}
	}
	return "", false
}
