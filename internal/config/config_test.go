package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withConfigDir points config lookups at a temp dir for the duration of a test.
func withConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("COOLIFY_TUI_CONFIG_DIR", dir)
	return dir
}

func TestLoadMissingReturnsErrNoConfig(t *testing.T) {
	withConfigDir(t)
	_, err := Load()
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("Load() error = %v, want ErrNoConfig", err)
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	dir := withConfigDir(t)

	cfg := New()
	inst := Instance{Name: "prod", URL: "https://coolify.example.com", Token: "42|secret"}
	if err := cfg.Upsert(inst); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	cfg.RefreshInterval = Duration(5 * time.Second)
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, FileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file mode = %#o, want 0600", perm)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(loaded.Instances))
	}
	got := loaded.Instances[0]
	if got.Name != "prod" || got.URL != "https://coolify.example.com" || got.Token != "42|secret" {
		t.Errorf("instance round-trip mismatch: %+v", got)
	}
	if loaded.RefreshInterval.Std() != 5*time.Second {
		t.Errorf("RefreshInterval = %v, want 5s", loaded.RefreshInterval)
	}
	// The interval must round-trip as a readable string, not nanoseconds.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "refresh_interval: 5s") {
		t.Errorf("config should store a human duration, got:\n%s", raw)
	}
	if loaded.ActiveInstance != "prod" {
		t.Errorf("ActiveInstance = %q, want prod", loaded.ActiveInstance)
	}
	if warn := loaded.PermissionWarning(); warn != "" {
		t.Errorf("PermissionWarning() = %q, want empty for a 0600 file", warn)
	}
}

func TestSaveIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	dir := withConfigDir(t)
	cfg := New()
	if err := cfg.Upsert(Instance{Name: "a", URL: "example.com", Token: "1|s"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != FileName {
			t.Errorf("leftover file in config dir: %q", e.Name())
		}
	}
}

func TestPermissionWarningOnLooseMode(t *testing.T) {
	dir := withConfigDir(t)
	path := filepath.Join(dir, FileName)
	body := "instances:\n  - name: prod\n    url: example.com\n    token: 1|s\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	warn := cfg.PermissionWarning()
	if warn == "" {
		t.Fatal("PermissionWarning() = empty, want a warning for a 0644 config")
	}
	if !strings.Contains(warn, "chmod 600") {
		t.Errorf("warning should suggest a fix, got %q", warn)
	}
	if strings.Contains(warn, "1|s") {
		t.Errorf("warning leaks the token: %q", warn)
	}
}

func TestDefaultsApplied(t *testing.T) {
	dir := withConfigDir(t)
	path := filepath.Join(dir, FileName)
	body := "instances:\n  - name: only\n    url: example.com\n    token: 1|s\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RefreshInterval.Std() != DefaultRefreshInterval {
		t.Errorf("RefreshInterval = %v, want default", cfg.RefreshInterval)
	}
	if cfg.LogLines != DefaultLogLines {
		t.Errorf("LogLines = %d, want default", cfg.LogLines)
	}
	if cfg.ActiveInstance != "only" {
		t.Errorf("ActiveInstance = %q, want the sole instance", cfg.ActiveInstance)
	}
	if !cfg.ShouldConfirmDestructive() {
		t.Error("ShouldConfirmDestructive() = false, want true by default")
	}
}

func TestConfirmDestructiveCanBeDisabled(t *testing.T) {
	off := false
	cfg := &Config{ConfirmDestructive: &off}
	if cfg.ShouldConfirmDestructive() {
		t.Error("ShouldConfirmDestructive() = true, want false when explicitly disabled")
	}
}

func TestResolveTokenFromEnv(t *testing.T) {
	t.Setenv("MY_COOLIFY_TOKEN", "77|from-env")
	inst := Instance{Name: "prod", URL: "example.com", TokenEnv: "MY_COOLIFY_TOKEN"}
	token, err := inst.ResolveToken()
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != "77|from-env" {
		t.Errorf("token = %q, want the env value", token)
	}
}

func TestResolveTokenEnvWinsOverInlineToken(t *testing.T) {
	t.Setenv("MY_COOLIFY_TOKEN", "77|from-env")
	inst := Instance{Name: "prod", URL: "example.com", Token: "1|inline", TokenEnv: "MY_COOLIFY_TOKEN"}
	token, err := inst.ResolveToken()
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != "77|from-env" {
		t.Errorf("token = %q, want token_env to take precedence", token)
	}
}

func TestResolveTokenMissingEnvIsAnError(t *testing.T) {
	inst := Instance{Name: "prod", URL: "example.com", TokenEnv: "DEFINITELY_UNSET_TOKEN_VAR"}
	if _, err := inst.ResolveToken(); err == nil {
		t.Fatal("want error when token_env is unset, got nil")
	}
}

func TestResolveTokenNoneConfigured(t *testing.T) {
	inst := Instance{Name: "prod", URL: "example.com"}
	if _, err := inst.ResolveToken(); err == nil {
		t.Fatal("want error when no token is configured, got nil")
	}
}

func TestTokenEnvKeepsTokenOutOfFile(t *testing.T) {
	withConfigDir(t)
	cfg := New()
	if err := cfg.Upsert(Instance{Name: "prod", URL: "example.com", TokenEnv: "MY_TOKEN"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(cfg.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "token:") && !strings.Contains(string(data), "token_env:") {
		t.Errorf("config should record token_env, not token:\n%s", data)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		inst    Instance
		wantErr bool
	}{
		{"ok inline", Instance{Name: "a", URL: "u", Token: "1|s"}, false},
		{"ok env", Instance{Name: "a", URL: "u", TokenEnv: "V"}, false},
		{"no name", Instance{URL: "u", Token: "1|s"}, true},
		{"blank name", Instance{Name: "   ", URL: "u", Token: "1|s"}, true},
		{"no url", Instance{Name: "a", Token: "1|s"}, true},
		{"no token", Instance{Name: "a", URL: "u"}, true},
	}
	for _, tc := range tests {
		err := tc.inst.Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: Validate() error = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestUpsertReplacesByName(t *testing.T) {
	cfg := New()
	if err := cfg.Upsert(Instance{Name: "prod", URL: "old.example.com", Token: "1|s"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := cfg.Upsert(Instance{Name: "PROD", URL: "new.example.com", Token: "2|s"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %d, want 1 (name match is case-insensitive)", len(cfg.Instances))
	}
	if cfg.Instances[0].URL != "new.example.com" {
		t.Errorf("URL = %q, want the replacement", cfg.Instances[0].URL)
	}
}

func TestUpsertRejectsInvalid(t *testing.T) {
	cfg := New()
	if err := cfg.Upsert(Instance{Name: "", URL: "u", Token: "1|s"}); err == nil {
		t.Fatal("want error for invalid instance, got nil")
	}
	if len(cfg.Instances) != 0 {
		t.Error("invalid instance should not be stored")
	}
}

func TestRemoveRepointsActive(t *testing.T) {
	cfg := New()
	for _, name := range []string{"a", "b"} {
		if err := cfg.Upsert(Instance{Name: name, URL: "u", Token: "1|s"}); err != nil {
			t.Fatalf("Upsert %s: %v", name, err)
		}
	}
	if cfg.ActiveInstance != "a" {
		t.Fatalf("ActiveInstance = %q, want a", cfg.ActiveInstance)
	}
	if err := cfg.Remove("a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if cfg.ActiveInstance != "b" {
		t.Errorf("ActiveInstance = %q, want b after removing the active instance", cfg.ActiveInstance)
	}
	if err := cfg.Remove("nope"); err == nil {
		t.Error("Remove of unknown instance should error")
	}
	if err := cfg.Remove("b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if cfg.ActiveInstance != "" {
		t.Errorf("ActiveInstance = %q, want empty when no instances remain", cfg.ActiveInstance)
	}
}

func TestActiveAndSetActive(t *testing.T) {
	cfg := New()
	if _, ok := cfg.Active(); ok {
		t.Error("Active() on empty config = true, want false")
	}
	for _, name := range []string{"a", "b"} {
		if err := cfg.Upsert(Instance{Name: name, URL: "u", Token: "1|s"}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	if err := cfg.SetActive("b"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	active, ok := cfg.Active()
	if !ok || active.Name != "b" {
		t.Errorf("Active() = (%+v, %v), want b", active, ok)
	}
	if err := cfg.SetActive("missing"); err == nil {
		t.Error("SetActive on unknown name should error")
	}

	// A dangling active_instance must still resolve to something usable.
	cfg.ActiveInstance = "gone"
	active, ok = cfg.Active()
	if !ok || active.Name != "a" {
		t.Errorf("Active() with dangling name = (%+v, %v), want first instance", active, ok)
	}
}

func TestNames(t *testing.T) {
	cfg := New()
	for _, name := range []string{"x", "y"} {
		if err := cfg.Upsert(Instance{Name: name, URL: "u", Token: "1|s"}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	got := cfg.Names()
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Errorf("Names() = %v, want [x y]", got)
	}
}

func TestRedactedToken(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "(unset)"},
		{"42|abcdefghijklmnopqrs", "42|************"},
		{"42|abc", "42|****"},
	}
	for _, tc := range tests {
		if got := RedactedToken(tc.in); got != tc.want {
			t.Errorf("RedactedToken(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A malformed token must not be echoed back in any form.
	if got := RedactedToken("no-pipe-here"); strings.Contains(got, "pipe") {
		t.Errorf("RedactedToken leaked input: %q", got)
	}
}

func TestDirHonoursXDG(t *testing.T) {
	t.Setenv("COOLIFY_TUI_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join("/tmp/xdg-test", AppName); dir != want {
		t.Errorf("Dir() = %q, want %q", dir, want)
	}
}
