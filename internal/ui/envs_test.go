package ui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// secretValue is a distinctive string, so a leak into the rendered view is
// unambiguous.
const secretValue = "sk-live-DEADBEEFCAFEBABE-do-not-render"

func envsServer(t *testing.T, vars []map[string]any) *recordingServer {
	t.Helper()
	return newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/applications/a1/envs" {
			_ = json.NewEncoder(w).Encode(vars)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
}

// openEnvTab switches to the Env tab and runs the resulting fetch.
func openEnvTab(t *testing.T, m Dashboard) Dashboard {
	t.Helper()
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	m = model.(Dashboard)
	if m.tab != tabEnv {
		t.Fatalf("tab = %v, want tabEnv", m.tab)
	}
	return applyCmd(t, m, cmd)
}

func TestEnvsMaskedByDefault(t *testing.T) {
	rec := envsServer(t, []map[string]any{
		{"uuid": "e1", "key": "STRIPE_SECRET", "value": secretValue, "is_runtime": true},
		{"uuid": "e2", "key": "PORT", "value": "3000", "is_runtime": true},
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)

	if len(m.envs.vars) != 2 {
		t.Fatalf("vars = %d, want 2", len(m.envs.vars))
	}

	view := m.View()
	// Keys are not secret and must be visible; values must not be.
	if !strings.Contains(view, "STRIPE_SECRET") {
		t.Error("variable names should be shown")
	}
	if strings.Contains(view, secretValue) {
		t.Errorf("a secret value is rendered unmasked:\n%s", view)
	}
	if !strings.Contains(view, "•") {
		t.Error("values should be masked with dots")
	}
	// The length is a useful diagnostic and is not itself secret.
	if !strings.Contains(view, "chars)") {
		t.Error("the mask should report the value's length")
	}
}

func TestEnvsRevealOnlyTheSelectedValue(t *testing.T) {
	rec := envsServer(t, []map[string]any{
		{"uuid": "e1", "key": "FIRST", "value": "first-value-visible"},
		{"uuid": "e2", "key": "SECOND", "value": secretValue},
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)

	// Cursor starts on the first variable.
	m = pressDash(t, m, "v")
	view := m.View()
	if !strings.Contains(view, "first-value-visible") {
		t.Errorf("v should reveal the selected value:\n%s", view)
	}
	if strings.Contains(view, secretValue) {
		t.Error("v must reveal only the selected value, not every value")
	}

	// v again re-masks it.
	m = pressDash(t, m, "v")
	if strings.Contains(m.View(), "first-value-visible") {
		t.Error("v should toggle the reveal off again")
	}
}

func TestEnvsRevealAllWarnsAboutScreenSharing(t *testing.T) {
	rec := envsServer(t, []map[string]any{
		{"uuid": "e1", "key": "FIRST", "value": "one"},
		{"uuid": "e2", "key": "SECOND", "value": secretValue},
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)

	m = pressDash(t, m, "V")
	view := m.View()
	if !strings.Contains(view, secretValue) {
		t.Error("V should reveal every value")
	}
	// Revealing everything at once is the risky mode, so it must be visible that
	// it is on.
	if !strings.Contains(view, "sharing this screen") {
		t.Errorf("reveal-all should warn about screen sharing:\n%s", view)
	}

	m = pressDash(t, m, "V")
	if strings.Contains(m.View(), secretValue) {
		t.Error("V should toggle reveal-all back off")
	}
}

func TestEnvsRevealDoesNotCarryToAnotherApplication(t *testing.T) {
	rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/applications/a1/envs":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"uuid": "e1", "key": "SHARED_KEY", "value": secretValue},
			})
		case "/api/v1/applications/a2/envs":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"uuid": "e9", "key": "SHARED_KEY", "value": "other-app-secret"},
			})
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)
	m = pressDash(t, m, "V")
	if !strings.Contains(m.View(), secretValue) {
		t.Fatal("reveal-all should be active")
	}

	// Move to another application: a value revealed for one application must not
	// be revealed for the next.
	m = pressDash(t, m, "h", "up")
	if m.envs.revealAll {
		t.Error("reveal-all must not carry across applications")
	}
	if len(m.envs.revealed) != 0 {
		t.Error("per-variable reveals must not carry across applications")
	}
}

func TestEnvsMultilineValueIsCollapsed(t *testing.T) {
	key := "-----BEGIN PRIVATE KEY-----\nline2\nline3\n-----END PRIVATE KEY-----"
	rec := envsServer(t, []map[string]any{
		{"uuid": "e1", "key": "SSH_KEY", "value": key, "is_multiline": true},
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)
	m = pressDash(t, m, "V")

	view := m.View()
	// A multi-line value must not take over the pane, and must not break the
	// layout by injecting newlines into a row.
	if strings.Contains(view, "BEGIN PRIVATE KEY-----\n") {
		t.Error("a multiline value should be collapsed onto one row")
	}
	if !strings.Contains(view, "multiline") {
		t.Error("a multiline variable should be flagged as such")
	}
	assertFitsTerminal(t, view, 120, 34)
}

func TestEnvsScopeFlags(t *testing.T) {
	rec := envsServer(t, []map[string]any{
		{"uuid": "e1", "key": "BUILD_ONLY", "value": "x", "is_buildtime": true},
		{"uuid": "e2", "key": "RUNTIME_ONLY", "value": "x", "is_runtime": true},
		{"uuid": "e3", "key": "SHARED_VAR", "value": "x", "is_shared": true, "is_runtime": true},
		{"uuid": "e4", "key": "PREVIEW_VAR", "value": "x", "is_preview": true, "is_runtime": true},
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)

	view := m.View()
	// Scope explains why a variable might not be present at runtime, which is a
	// common source of confusion.
	for _, want := range []string{"build-only", "runtime", "shared", "preview"} {
		if !strings.Contains(view, want) {
			t.Errorf("scope flag %q missing:\n%s", want, view)
		}
	}
}

func TestEnvsEmptyValueIsLabelled(t *testing.T) {
	rec := envsServer(t, []map[string]any{
		{"uuid": "e1", "key": "UNSET", "value": ""},
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)

	// An empty value is a common misconfiguration and should be obvious rather
	// than looking like a mask of unknown length.
	if !strings.Contains(m.View(), "(empty)") {
		t.Errorf("an empty value should be labelled:\n%s", m.View())
	}
}

func TestEnvsPrefersRealValue(t *testing.T) {
	rec := envsServer(t, []map[string]any{
		{"uuid": "e1", "key": "DB_URL", "value": "{{SHARED_DB}}", "real_value": "postgres://resolved"},
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)
	m = pressDash(t, m, "V")

	// real_value has shared references expanded, which is what actually reaches
	// the container.
	if !strings.Contains(m.View(), "postgres://resolved") {
		t.Errorf("real_value should be preferred:\n%s", m.View())
	}
}

func TestEnvsPermissionErrorExplainsItself(t *testing.T) {
	rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/envs") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Missing permission."}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)

	view := m.View()
	if !strings.Contains(view, "read:sensitive") {
		t.Errorf("a 403 should name the permission needed:\n%s", view)
	}
	// It should also make clear this is not fatal to the rest of the tool.
	if !strings.Contains(view, "monitor and deploy") {
		t.Error("the message should say a read-only token still works elsewhere")
	}
}

func TestEnvsEmptyState(t *testing.T) {
	rec := envsServer(t, []map[string]any{})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)

	if !strings.Contains(m.View(), "No environment variables set") {
		t.Errorf("an empty list should say so:\n%s", m.View())
	}
}

func TestEnvsNavigation(t *testing.T) {
	rec := envsServer(t, []map[string]any{
		{"uuid": "e1", "key": "A", "value": "1"},
		{"uuid": "e2", "key": "B", "value": "2"},
		{"uuid": "e3", "key": "C", "value": "3"},
	})
	m := newActionDashboard(t, rec, "web")
	m = openEnvTab(t, m)

	if m.envs.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.envs.cursor)
	}
	m = pressDash(t, m, "down", "down")
	if m.envs.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.envs.cursor)
	}
	// Clamped at the ends.
	m = pressDash(t, m, "down", "down")
	if m.envs.cursor != 2 {
		t.Errorf("cursor = %d, want it clamped at 2", m.envs.cursor)
	}
	m = pressDash(t, m, "g")
	if m.envs.cursor != 0 {
		t.Errorf("g should go to the first variable, got %d", m.envs.cursor)
	}
	m = pressDash(t, m, "G")
	if m.envs.cursor != 2 {
		t.Errorf("G should go to the last variable, got %d", m.envs.cursor)
	}
}

func TestEnvsStaleResponseIgnored(t *testing.T) {
	rec := envsServer(t, []map[string]any{{"uuid": "e1", "key": "A", "value": "1"}})
	m := newActionDashboard(t, rec, "web")
	m.envs = envsState{appUUID: "a4", revealed: map[string]bool{}}

	model, _ := m.Update(envsMsg{
		appUUID: "a1",
		vars:    []coolify.EnvVar{{Key: "STALE"}},
	})
	m = model.(Dashboard)

	if len(m.envs.vars) != 0 {
		t.Error("a response for a different application must be discarded")
	}
}

func TestMaskEnvValue(t *testing.T) {
	tests := []struct {
		in       string
		contains string
		absent   string
	}{
		{"", "(empty)", ""},
		{"abc", "(3 chars)", "abc"},
		{strings.Repeat("x", 64), "(64 chars)", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
	}
	for _, tc := range tests {
		got := maskEnvValue(tc.in)
		if !strings.Contains(got, tc.contains) {
			t.Errorf("maskEnvValue(%q) = %q, want it to contain %q", tc.in, got, tc.contains)
		}
		if tc.absent != "" && strings.Contains(got, tc.absent) {
			t.Errorf("maskEnvValue(%q) = %q, leaks the value", tc.in, got)
		}
	}
	// The mask must never grow with the secret beyond a bounded number of dots,
	// so a long value's length is not inferable from the dot count alone.
	long := maskEnvValue(strings.Repeat("y", 500))
	if strings.Count(long, "•") > 12 {
		t.Errorf("mask has %d dots, want at most 12", strings.Count(long, "•"))
	}
}

func TestEnvKeyFallsBackToName(t *testing.T) {
	// Coolify does not always populate uuid on env vars; without a fallback the
	// reveal map would collide across variables.
	if got := envKey(coolify.EnvVar{UUID: "u1", Key: "K"}); got != "u1" {
		t.Errorf("envKey with uuid = %q, want u1", got)
	}
	if got := envKey(coolify.EnvVar{Key: "K"}); got != "K" {
		t.Errorf("envKey without uuid = %q, want the key name", got)
	}
}

func TestEnvsViewFitsTerminal(t *testing.T) {
	vars := make([]map[string]any, 0, 40)
	for i := range 40 {
		vars = append(vars, map[string]any{
			"uuid":       "e" + string(rune('a'+i%26)),
			"key":        strings.Repeat("VERY_LONG_VARIABLE_NAME_", 2) + string(rune('A'+i%26)),
			"value":      strings.Repeat("value", 30),
			"is_runtime": true,
		})
	}
	rec := envsServer(t, vars)

	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {60, 16}} {
		m := newActionDashboard(t, rec, "web")
		model, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = model.(Dashboard)
		for i, row := range m.tree.rows {
			if row.kind == rowApp && row.app.Name == "web" {
				m.tree.cursor = i
			}
		}
		m = openEnvTab(t, m)
		assertFitsTerminal(t, m.View(), size.w, size.h)

		m = pressDash(t, m, "V")
		assertFitsTerminal(t, m.View(), size.w, size.h)
	}
}
