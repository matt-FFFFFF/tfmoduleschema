package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncodeHostForEnv(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"registry.example.com", "registry_example_com"},
		{"my-registry.example.com", "my__registry_example_com"},
		{"registry.internal:8443", "registry_internal_8443"},
		{"app.terraform.io", "app_terraform_io"},
		{"host", "host"},
	}
	for _, c := range cases {
		if got := encodeHostForEnv(c.in); got != c.want {
			t.Errorf("encodeHostForEnv(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCandidateEnvKeys(t *testing.T) {
	// Bare host: single key.
	if got := candidateEnvKeys("registry.example.com"); len(got) != 1 || got[0] != "TF_TOKEN_registry_example_com" {
		t.Errorf("bare host keys: %v", got)
	}
	// Host with port: two keys, exact first, bare fallback second.
	got := candidateEnvKeys("registry.internal:8443")
	want := []string{"TF_TOKEN_registry_internal_8443", "TF_TOKEN_registry_internal"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("host:port keys: got %v, want %v", got, want)
	}
}

func TestResolveTokenForHost_Env(t *testing.T) {
	// Isolate: clear credentials-file envs so we only exercise env
	// lookup, and set a bogus HOME to avoid reading the real user's
	// credentials file.
	withIsolatedTokenEnv(t)

	t.Setenv("TF_TOKEN_registry_example_com", "env-token")
	tok, err := ResolveTokenForHost("registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "env-token" {
		t.Errorf("got %q, want env-token", tok)
	}

	// Case-insensitive host input should still find the same env var
	// (env key itself is case-sensitive on Unix, but we lowercase
	// the host before deriving it).
	tok, err = ResolveTokenForHost("Registry.Example.COM")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "env-token" {
		t.Errorf("case-insensitive: got %q, want env-token", tok)
	}
}

func TestResolveTokenForHost_EnvPrefersExactPort(t *testing.T) {
	withIsolatedTokenEnv(t)
	t.Setenv("TF_TOKEN_registry_internal", "bare")
	t.Setenv("TF_TOKEN_registry_internal_8443", "with-port")
	tok, err := ResolveTokenForHost("registry.internal:8443")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "with-port" {
		t.Errorf("got %q, want with-port", tok)
	}
}

func TestResolveTokenForHost_EnvFallsBackToBareHost(t *testing.T) {
	withIsolatedTokenEnv(t)
	t.Setenv("TF_TOKEN_registry_internal", "bare")
	tok, err := ResolveTokenForHost("registry.internal:8443")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "bare" {
		t.Errorf("got %q, want bare", tok)
	}
}

func TestResolveTokenForHost_File(t *testing.T) {
	withIsolatedTokenEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.tfrc.json")
	if err := os.WriteFile(path, []byte(`{
      "credentials": {
        "registry.example.com": {"token": "file-token"},
        "other.host": {"token": "nope"}
      }
    }`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TF_CLI_CONFIG_FILE", path)

	tok, err := ResolveTokenForHost("registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "file-token" {
		t.Errorf("got %q, want file-token", tok)
	}
}

func TestResolveTokenForHost_FileCaseInsensitiveKey(t *testing.T) {
	withIsolatedTokenEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.tfrc.json")
	if err := os.WriteFile(path, []byte(`{"credentials":{"Registry.Example.COM":{"token":"ci-token"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TF_CLI_CONFIG_FILE", path)

	tok, err := ResolveTokenForHost("registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ci-token" {
		t.Errorf("got %q, want ci-token", tok)
	}
}

func TestResolveTokenForHost_EnvBeatsFile(t *testing.T) {
	withIsolatedTokenEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.tfrc.json")
	if err := os.WriteFile(path, []byte(`{"credentials":{"registry.example.com":{"token":"file-token"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TF_CLI_CONFIG_FILE", path)
	t.Setenv("TF_TOKEN_registry_example_com", "env-token")

	tok, err := ResolveTokenForHost("registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "env-token" {
		t.Errorf("precedence: got %q, want env-token", tok)
	}
}

func TestResolveTokenForHost_MissingFileIsNotError(t *testing.T) {
	withIsolatedTokenEnv(t)
	t.Setenv("TF_CLI_CONFIG_FILE", filepath.Join(t.TempDir(), "does-not-exist.json"))
	tok, err := ResolveTokenForHost("registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		t.Errorf("got %q, want empty", tok)
	}
}

func TestResolveTokenForHost_MalformedFile(t *testing.T) {
	withIsolatedTokenEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.tfrc.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TF_CLI_CONFIG_FILE", path)

	_, err := ResolveTokenForHost("registry.example.com")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestResolveTokenForHost_EmptyHost(t *testing.T) {
	withIsolatedTokenEnv(t)
	tok, err := ResolveTokenForHost("")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		t.Errorf("got %q, want empty", tok)
	}
}

// withIsolatedTokenEnv unsets variables that would otherwise cause
// the token resolver to read a file outside the test's control. HOME
// is redirected to a temp dir so the default ~/.terraform.d path
// cannot produce a hit, and the Windows home-directory variables are
// also redirected since os.UserHomeDir consults USERPROFILE and
// HOMEDRIVE/HOMEPATH on that platform.
func withIsolatedTokenEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("TF_CLI_CONFIG_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in      string
		okWant  bool
		hWant   string
		pWant   string
	}{
		{"example.com:8443", true, "example.com", "8443"},
		{"example.com", false, "", ""},
		{":8443", false, "", ""},           // empty host rejected
		{"example.com:", false, "", ""},    // empty port rejected
		{"[::1]:443", false, "", ""},       // IPv6 literal unsupported
		{"", false, "", ""},
	}
	for _, tc := range cases {
		h, p, ok := splitHostPort(tc.in)
		if ok != tc.okWant || h != tc.hWant || p != tc.pWant {
			t.Errorf("splitHostPort(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, h, p, ok, tc.hWant, tc.pWant, tc.okWant)
		}
	}
}
