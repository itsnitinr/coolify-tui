package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/itsnitinr/coolify-tui/internal/config"
)

const testToken = "42|super-secret-token-value"

func newTestWizard(t *testing.T) OnboardModel {
	t.Helper()
	t.Setenv("COOLIFY_TUI_CONFIG_DIR", t.TempDir())
	return NewOnboardModel(config.New(), NewStyles(DefaultTheme()), false)
}

// press feeds a keystroke to the model and returns the updated model.
func press(t *testing.T, m OnboardModel, keys ...string) OnboardModel {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyMsg{Type: tea.KeyShiftTab}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		updated, _ := m.Update(msg)
		var ok bool
		m, ok = updated.(OnboardModel)
		if !ok {
			t.Fatalf("Update returned %T, want OnboardModel", updated)
		}
	}
	return m
}

// send feeds a non-key message and returns the updated model plus its command.
func send(t *testing.T, m OnboardModel, msg tea.Msg) (OnboardModel, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	out, ok := updated.(OnboardModel)
	if !ok {
		t.Fatalf("Update returned %T, want OnboardModel", updated)
	}
	return out, cmd
}

func TestWizardStartsAtIntro(t *testing.T) {
	m := newTestWizard(t)
	if m.step != stepIntro {
		t.Errorf("step = %v, want stepIntro", m.step)
	}
	view := m.View()
	for _, want := range []string{"coolify-tui", "API token", "Security"} {
		if !strings.Contains(view, want) {
			t.Errorf("intro view missing %q", want)
		}
	}
}

func TestWizardIntroExplainsWhereTheTokenGoes(t *testing.T) {
	m := newTestWizard(t)
	if !strings.Contains(m.View(), "0600") {
		t.Error("intro should tell the user the token is stored at mode 0600")
	}
}

func TestWizardAdvancesThroughFields(t *testing.T) {
	m := newTestWizard(t)

	m = press(t, m, "enter")
	if m.step != stepName {
		t.Fatalf("after intro enter: step = %v, want stepName", m.step)
	}

	m = press(t, m, "p", "r", "o", "d", "enter")
	if m.step != stepURL {
		t.Fatalf("after name: step = %v, want stepURL", m.step)
	}
	if got := m.inputs[fieldName].Value(); got != "prod" {
		t.Errorf("name = %q, want prod", got)
	}

	// An empty URL must not advance: there is nothing to connect to.
	m = press(t, m, "enter")
	if m.step != stepURL {
		t.Errorf("empty URL advanced to %v, want to stay on stepURL", m.step)
	}

	m = press(t, m, "x", ".", "c", "o", "m", "enter")
	if m.step != stepToken {
		t.Fatalf("after URL: step = %v, want stepToken", m.step)
	}

	// An empty token must not advance either.
	m = press(t, m, "enter")
	if m.step != stepToken {
		t.Errorf("empty token advanced to %v, want to stay on stepToken", m.step)
	}
}

func TestWizardEmptyNameGetsDefault(t *testing.T) {
	m := newTestWizard(t)
	m = press(t, m, "enter", "enter")
	if m.step != stepURL {
		t.Fatalf("step = %v, want stepURL", m.step)
	}
	if got := m.inputs[fieldName].Value(); got != "default" {
		t.Errorf("name = %q, want the default rather than a blocked form", got)
	}
}

func TestWizardEscBacksOut(t *testing.T) {
	m := newTestWizard(t)
	m = press(t, m, "enter", "a", "enter", "b", "enter")
	if m.step != stepToken {
		t.Fatalf("step = %v, want stepToken", m.step)
	}
	m = press(t, m, "esc")
	if m.step != stepURL {
		t.Errorf("esc from token: step = %v, want stepURL", m.step)
	}
	m = press(t, m, "esc")
	if m.step != stepName {
		t.Errorf("esc from URL: step = %v, want stepName", m.step)
	}
	m = press(t, m, "esc")
	if m.step != stepIntro {
		t.Errorf("esc from name: step = %v, want stepIntro", m.step)
	}
}

func TestWizardEscFromIntroCancels(t *testing.T) {
	m := newTestWizard(t)
	m = press(t, m, "esc")
	if !m.Result().Cancelled {
		t.Error("esc at intro should cancel")
	}
}

func TestWizardTabCyclesFields(t *testing.T) {
	m := newTestWizard(t)
	m = press(t, m, "enter")
	if m.focus != fieldName {
		t.Fatalf("focus = %d, want fieldName", m.focus)
	}
	m = press(t, m, "tab")
	if m.focus != fieldURL {
		t.Errorf("after tab: focus = %d, want fieldURL", m.focus)
	}
	m = press(t, m, "tab", "tab")
	if m.focus != fieldToken {
		t.Errorf("focus = %d, want fieldToken (clamped at the last field)", m.focus)
	}
	m = press(t, m, "shift+tab")
	if m.focus != fieldURL {
		t.Errorf("after shift+tab: focus = %d, want fieldURL", m.focus)
	}
}

func TestWizardNeverRendersTokenInPlaintext(t *testing.T) {
	m := newTestWizard(t)
	m = press(t, m, "enter", "p", "enter", "x", ".", "c", "o", "m", "enter")
	if m.step != stepToken {
		t.Fatalf("step = %v, want stepToken", m.step)
	}
	for _, r := range testToken {
		m = press(t, m, string(r))
	}
	if got := m.inputs[fieldToken].Value(); got != testToken {
		t.Fatalf("token field = %q, want the typed token", got)
	}

	// The token must not be legible at any step that can display it.
	for _, step := range []onboardStep{stepToken, stepVerifying, stepResult} {
		m.step = step
		if view := m.View(); strings.Contains(view, "super-secret-token-value") {
			t.Errorf("step %v renders the token in plaintext:\n%s", step, view)
		}
	}
}

func TestWizardProbeSuccessSavesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COOLIFY_TUI_CONFIG_DIR", dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			_, _ = w.Write([]byte("4.0.0-beta.397"))
		case "/api/v1/servers":
			_, _ = w.Write([]byte(`[{"uuid":"s1","name":"prod-1","settings":{"is_reachable":true,"is_usable":true}}]`))
		case "/api/v1/applications":
			_, _ = w.Write([]byte(`[{"uuid":"a1","name":"web","status":"running:healthy"}]`))
		case "/api/v1/applications/a1/envs":
			_, _ = w.Write([]byte(`[{"key":"PORT","value":"3000"}]`))
		case "/api/v1/teams":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Root Team"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := config.New()
	m := NewOnboardModel(cfg, NewStyles(DefaultTheme()), false)
	m.inputs[fieldName].SetValue("prod")
	m.inputs[fieldURL].SetValue(srv.URL)
	m.inputs[fieldToken].SetValue(testToken)

	// Run the probe command synchronously.
	cmd := probeInstance(m.instance())
	msg := cmd()
	probe, ok := msg.(probeMsg)
	if !ok {
		t.Fatalf("probe returned %T, want probeMsg", msg)
	}
	if probe.err != nil {
		t.Fatalf("probe error: %v", probe.err)
	}
	if probe.version != "4.0.0-beta.397" {
		t.Errorf("version = %q", probe.version)
	}
	if probe.servers != 1 || probe.apps != 1 {
		t.Errorf("inventory = %d servers, %d apps; want 1 and 1", probe.servers, probe.apps)
	}

	// read and read:sensitive should both be detected as granted.
	perms := map[string]permissionCheck{}
	for _, p := range probe.permissions {
		perms[p.name] = p
	}
	if !perms["read"].ok {
		t.Errorf("read permission not detected: %+v", perms["read"])
	}
	if !perms["read:sensitive"].ok {
		t.Errorf("read:sensitive not detected: %+v", perms["read:sensitive"])
	}
	if perms["write"].ok || perms["deploy"].ok {
		t.Error("write/deploy must not be claimed as verified: probing them has side effects")
	}

	// Feeding the probe result should trigger a save.
	m, saveCmd := send(t, m, probe)
	if m.step != stepResult {
		t.Fatalf("step = %v, want stepResult", m.step)
	}
	if saveCmd == nil {
		t.Fatal("successful probe should return a save command")
	}
	saved, ok := saveCmd().(savedMsg)
	if !ok {
		t.Fatal("save command should return savedMsg")
	}
	if saved.err != nil {
		t.Fatalf("save failed: %v", saved.err)
	}

	m, _ = send(t, m, saved)
	if !m.Result().Saved {
		t.Error("Result().Saved = false after a successful save")
	}
	if m.Result().Instance.Name != "prod" {
		t.Errorf("saved instance = %q, want prod", m.Result().Instance.Name)
	}

	// The config on disk must be usable and locked down.
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load after wizard: %v", err)
	}
	if loaded.ActiveInstance != "prod" {
		t.Errorf("active instance = %q, want prod", loaded.ActiveInstance)
	}
	if warn := loaded.PermissionWarning(); warn != "" {
		t.Errorf("saved config is over-permissive: %s", warn)
	}
	inst, ok := loaded.Instance("prod")
	if !ok {
		t.Fatal("instance prod not found in saved config")
	}
	if inst.Token != testToken {
		t.Errorf("token did not round-trip")
	}
}

func TestWizardProbeDetectsMissingSensitivePermission(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			_, _ = w.Write([]byte("4.0.0"))
		case "/api/v1/servers":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/applications":
			_, _ = w.Write([]byte(`[{"uuid":"a1","name":"web"}]`))
		case "/api/v1/applications/a1/envs":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Missing required permission."}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	msg := probeInstance(config.Instance{Name: "x", URL: srv.URL, Token: testToken})()
	probe := msg.(probeMsg)
	if probe.err != nil {
		t.Fatalf("a missing optional permission must not fail the probe: %v", probe.err)
	}
	for _, p := range probe.permissions {
		if p.name != "read:sensitive" {
			continue
		}
		if p.ok {
			t.Error("read:sensitive reported as granted despite a 403")
		}
		if !strings.Contains(p.detail, "not granted") {
			t.Errorf("detail = %q, want it to say the permission is missing", p.detail)
		}
	}
}

func TestWizardProbeRejectsBadToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthenticated."}`))
	}))
	defer srv.Close()

	msg := probeInstance(config.Instance{Name: "x", URL: srv.URL, Token: "42|wrong"})()
	probe := msg.(probeMsg)
	if probe.err == nil {
		t.Fatal("want an error for a rejected token")
	}
	if !strings.Contains(probe.err.Error(), "Security") {
		t.Errorf("error should point at token creation, got: %v", probe.err)
	}
}

func TestWizardProbeFailureKeepsUserOnResultThenReturnsToForm(t *testing.T) {
	m := newTestWizard(t)
	m.inputs[fieldName].SetValue("prod")
	m.inputs[fieldURL].SetValue("x.example")
	m.inputs[fieldToken].SetValue(testToken)

	m, cmd := send(t, m, probeMsg{err: errProbeTest})
	if m.step != stepResult {
		t.Fatalf("step = %v, want stepResult", m.step)
	}
	if cmd != nil {
		t.Error("a failed probe must not trigger a save")
	}
	if m.Result().Saved {
		t.Error("Saved = true after a failed probe")
	}
	if !strings.Contains(m.View(), "Could not connect") {
		t.Error("failure view should say the connection failed")
	}
	// Enter should hand the user back to the form to fix the details.
	m = press(t, m, "enter")
	if m.step != stepToken {
		t.Errorf("step = %v, want stepToken so the user can correct the token", m.step)
	}
}

func TestDescribeProbeErrorAddsHints(t *testing.T) {
	inst := config.Instance{Name: "x", URL: "coolify.example.com"}
	tests := []struct {
		raw  string
		want string
	}{
		{"x509: certificate signed by unknown authority", "insecure_skip_verify"},
		{"dial tcp: lookup foo: no such host", "did not resolve"},
		{"dial tcp 127.0.0.1:80: connect: connection refused", "non-standard"},
		{"context deadline exceeded", "timed out"},
	}
	for _, tc := range tests {
		got := describeProbeError(inst, errString(tc.raw))
		if !strings.Contains(got.Error(), tc.want) {
			t.Errorf("describeProbeError(%q) = %q, want it to mention %q", tc.raw, got, tc.want)
		}
	}
}

func TestWrapAndIndent(t *testing.T) {
	got := wrap("one two three four five", 9)
	if got != "one two\nthree\nfour five" {
		t.Errorf("wrap() = %q", got)
	}
	if got := wrap("a\n\nb", 10); got != "a\n\nb" {
		t.Errorf("wrap should preserve blank lines, got %q", got)
	}
	if got := indent("a\nb", "> "); got != "> a\n> b" {
		t.Errorf("indent() = %q", got)
	}
}

// errString is a minimal error for table-driven tests.
type errString string

func (e errString) Error() string { return string(e) }

var errProbeTest = errString("probe failed: connection refused")
