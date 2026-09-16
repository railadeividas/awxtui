package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envMap turns a map into the lookup Resolve expects.
func envMap(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func writeConfig(t *testing.T, body string, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		t.Fatal(err)
	}
	return path
}

const sample = `
default: prod
instances:
  prod:
    url: https://awx.example.com
    token: prod-token
    read_only: true
  staging:
    url: https://awx-staging.example.com
    token: staging-token
    insecure: true
`

func TestLoadAndNames(t *testing.T) {
	f, err := Load(writeConfig(t, sample, 0o600))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Names(); len(got) != 2 || got[0] != "prod" || got[1] != "staging" {
		t.Errorf("names = %v, want [prod staging] sorted", got)
	}
	if f.Instances["prod"].Name != "prod" {
		t.Error("instances should know their own name")
	}
	if !f.Instances["prod"].ReadOnly {
		t.Error("prod should be read-only")
	}
	if f.InsecurePerms {
		t.Error("a 0600 file should not be flagged")
	}
}

// A missing config file is fine: the environment alone is a valid setup.
func TestLoadMissingFileIsNotAnError(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "absent.yml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(f.Instances) != 0 {
		t.Error("expected no instances")
	}
}

func TestResolvePrecedence(t *testing.T) {
	f, err := Load(writeConfig(t, sample, 0o600))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}

	// 1. explicit request wins
	got, err := Resolve(f, "staging", envMap(env))
	if err != nil || got.Name != "staging" {
		t.Errorf("explicit request gave %q (%v), want staging", got.Name, err)
	}

	// 2. AWXTUI_INSTANCE
	got, _ = Resolve(f, "", envMap(map[string]string{"AWXTUI_INSTANCE": "staging"}))
	if got.Name != "staging" {
		t.Errorf("AWXTUI_INSTANCE gave %q, want staging", got.Name)
	}

	// 3. AWX_URL/AWX_TOKEN are an instance of their own
	got, _ = Resolve(f, "", envMap(map[string]string{
		"AWX_URL": "https://from-env", "AWX_TOKEN": "env-token"}))
	if got.Name != "env" || got.URL != "https://from-env" {
		t.Errorf("env vars gave %q/%q, want the env instance", got.Name, got.URL)
	}

	// 4. the config default
	got, _ = Resolve(f, "", envMap(env))
	if got.Name != "prod" {
		t.Errorf("default gave %q, want prod", got.Name)
	}

	// An explicit request beats the environment.
	got, _ = Resolve(f, "prod", envMap(map[string]string{
		"AWXTUI_INSTANCE": "staging", "AWX_URL": "https://from-env", "AWX_TOKEN": "x"}))
	if got.Name != "prod" {
		t.Errorf("explicit request gave %q, want prod to win", got.Name)
	}
}

// With a single instance and no default, use it.
func TestResolveSoleInstance(t *testing.T) {
	f, _ := Load(writeConfig(t, "instances:\n  only:\n    url: https://x\n    token: t\n", 0o600))
	got, err := Resolve(f, "", envMap(nil))
	if err != nil || got.Name != "only" {
		t.Errorf("sole instance gave %q (%v)", got.Name, err)
	}
}

// A config file can hold URLs while the token stays in the environment.
func TestResolveTokenFallsBackToEnv(t *testing.T) {
	f, _ := Load(writeConfig(t, "instances:\n  prod:\n    url: https://x\n", 0o600))
	got, err := Resolve(f, "prod", envMap(map[string]string{"AWX_TOKEN": "from-env"}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "from-env" {
		t.Errorf("token = %q, want the environment's", got.Token)
	}
}

// Environment switches still apply to a configured instance.
func TestResolveEnvEnablesReadOnlyAndInsecure(t *testing.T) {
	f, _ := Load(writeConfig(t, sample, 0o600))
	got, err := Resolve(f, "staging", envMap(map[string]string{"AWXTUI_READONLY": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !got.ReadOnly {
		t.Error("AWXTUI_READONLY should force read-only on any instance")
	}
	// The instance's own setting is not undone by an unset variable.
	got, _ = Resolve(f, "prod", envMap(nil))
	if !got.ReadOnly {
		t.Error("prod is configured read_only: true")
	}
}

func TestResolveErrors(t *testing.T) {
	f, _ := Load(writeConfig(t, sample, 0o600))

	_, err := Resolve(f, "nope", envMap(nil))
	if err == nil || !strings.Contains(err.Error(), "prod, staging") {
		t.Errorf("err = %v, want it to list the configured instances", err)
	}

	empty := File{}
	_, err = Resolve(empty, "", envMap(nil))
	if err == nil || !strings.Contains(err.Error(), "AWX_URL") {
		t.Errorf("err = %v, want it to explain how to configure one", err)
	}

	noToken, _ := Load(writeConfig(t, "instances:\n  x:\n    url: https://x\n", 0o600))
	_, err = Resolve(noToken, "x", envMap(nil))
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("err = %v, want a complaint about the missing token", err)
	}
}

// A token written in plain text in a world-readable file is worth warning about.
func TestInsecurePermissionsDetected(t *testing.T) {
	f, err := Load(writeConfig(t, sample, 0o644))
	if err != nil {
		t.Fatal(err)
	}
	if !f.InsecurePerms {
		t.Error("a 0644 file holding tokens should be flagged")
	}
	// A file with no literal tokens has nothing to leak.
	f, _ = Load(writeConfig(t, "instances:\n  x:\n    url: https://x\n    token_command: echo hi\n", 0o644))
	if f.InsecurePerms {
		t.Error("no literal token means nothing to warn about")
	}
}

func TestResolveTokenRunsCommand(t *testing.T) {
	inst := Instance{Name: "prod", TokenCommand: "printf 'secret-from-command\n'"}
	got, err := inst.ResolveToken()
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret-from-command" {
		t.Errorf("token = %q, want the command output trimmed", got)
	}

	failing := Instance{Name: "prod", TokenCommand: "exit 3"}
	if _, err := failing.ResolveToken(); err == nil {
		t.Error("a failing token_command should be reported")
	}
	silent := Instance{Name: "prod", TokenCommand: "true"}
	if _, err := silent.ResolveToken(); err == nil {
		t.Error("a token_command producing nothing should be reported")
	}
}

// Tokens read out of a shell env file arrive quoted; AWX rejects those.
func TestResolveTokenStripsSurroundingQuotes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`"quoted"`, "quoted"},
		{`'single'`, "single"},
		{`bare`, "bare"},
		{`mid"dle`, `mid"dle`},
	} {
		if got, _ := (Instance{Token: tc.in}).ResolveToken(); got != tc.want {
			t.Errorf("ResolveToken(%s) = %q, want %q", tc.in, got, tc.want)
		}
		cmd := Instance{TokenCommand: "printf '%s' " + shellQuote(tc.in)}
		if got, err := cmd.ResolveToken(); err != nil || got != tc.want {
			t.Errorf("token_command %s = %q (%v), want %q", tc.in, got, err, tc.want)
		}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestDefaultPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	if got, want := DefaultPath(), "/tmp/xdg/awxtui/config.yml"; got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}
