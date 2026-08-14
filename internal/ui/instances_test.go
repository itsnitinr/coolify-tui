package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/itsnitinr/coolify-tui/internal/config"
)

// withInstances adds extra instances to a dashboard's config.
func withInstances(t *testing.T, m Dashboard, instances ...config.Instance) Dashboard {
	t.Helper()
	for _, inst := range instances {
		if err := m.cfg.Upsert(inst); err != nil {
			t.Fatalf("Upsert %s: %v", inst.Name, err)
		}
	}
	return m
}

func TestInstancePickerNeedsMoreThanOneInstance(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m = withInstances(t, m, config.Instance{Name: "prod", URL: "a", Token: "1|s"})

	m = pressDash(t, m, "i")
	if m.picker.open {
		t.Error("the switcher should not open with a single instance")
	}
	if len(m.toasts) == 0 || !strings.Contains(m.toasts[0].message, "coolify-tui login") {
		t.Errorf("toasts = %+v, want a pointer to adding another instance", m.toasts)
	}
}

func TestInstancePickerOpensOnCurrentInstance(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m = withInstances(t, m,
		config.Instance{Name: "homelab", URL: "http://192.168.1.10:8000", TokenEnv: "HOMELAB_TOKEN"},
		config.Instance{Name: "prod", URL: "https://coolify.example.com", Token: "1|s"},
	)
	m.instance = config.Instance{Name: "prod"}

	m = pressDash(t, m, "i")
	if !m.picker.open {
		t.Fatal("i should open the switcher")
	}
	// The cursor starts on the instance you are already on, so enter is a no-op
	// rather than an accidental switch.
	if m.cfg.Instances[m.picker.cursor].Name != "prod" {
		t.Errorf("cursor on %q, want the current instance", m.cfg.Instances[m.picker.cursor].Name)
	}

	view := m.View()
	for _, want := range []string{"Switch instance", "homelab", "prod", "$HOMELAB_TOKEN"} {
		if !strings.Contains(view, want) {
			t.Errorf("the switcher should show %q:\n%s", want, view)
		}
	}
}

func TestInstancePickerIsModal(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m = withInstances(t, m,
		config.Instance{Name: "a", URL: "http://a", Token: "1|s"},
		config.Instance{Name: "b", URL: "http://b", Token: "1|s"},
	)
	cursorBefore := m.tree.cursor

	m = pressDash(t, m, "i")
	// Actions must not fire underneath the overlay.
	m = pressDash(t, m, "d", "/", "2")
	if m.pending != nil {
		t.Error("d should not start a deploy under the switcher")
	}
	if m.filtering {
		t.Error("/ should not open the filter under the switcher")
	}
	if m.tab != tabDetails {
		t.Error("tab switching should not work under the switcher")
	}
	if m.tree.cursor != cursorBefore {
		t.Error("the tree cursor should not move under the switcher")
	}
}

func TestInstancePickerEscCloses(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m = withInstances(t, m,
		config.Instance{Name: "a", URL: "http://a", Token: "1|s"},
		config.Instance{Name: "b", URL: "http://b", Token: "1|s"},
	)
	m = pressDash(t, m, "i", "esc")
	if m.picker.open {
		t.Error("esc should close the switcher")
	}
}

func TestSwitchInstanceResetsEverything(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m.instance = config.Instance{Name: "prod", URL: rec.server.URL, Token: "1|s"}
	m = withInstances(t, m,
		config.Instance{Name: "prod", URL: rec.server.URL, Token: "1|s"},
		config.Instance{Name: "other", URL: rec.server.URL, Token: "2|s"},
	)

	// Dirty every piece of per-instance state.
	m.deployments = deploymentsState{appUUID: "a1", openUUID: "dep-1"}
	m.logs = logsState{appUUID: "a1", text: "old instance output"}
	m.envs = envsState{appUUID: "a1", revealAll: true}
	m.inFlight["a1"] = actionDeploy
	m.tab = tabEnv

	m = pressDash(t, m, "i")
	m.picker.cursor = 1 // "other"
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Dashboard)

	if m.instance.Name != "other" {
		t.Fatalf("instance = %q, want other", m.instance.Name)
	}
	if m.picker.open {
		t.Error("the switcher should close after switching")
	}

	// Nothing may carry over: it all described a different Coolify install.
	if len(m.inv.Apps) != 0 || len(m.inv.Servers) != 0 {
		t.Error("the inventory should be cleared")
	}
	if m.deployments.appUUID != "" || m.deployments.showingLogs() {
		t.Error("deployment state should be cleared")
	}
	if m.logs.text != "" || m.logs.appUUID != "" {
		t.Error("log state should be cleared")
	}
	if m.envs.appUUID != "" || m.envs.revealAll {
		t.Error("env state and reveals should be cleared")
	}
	if len(m.inFlight) != 0 {
		t.Error("in-flight markers belong to the old instance and should be cleared")
	}
	if m.tab != tabDetails {
		t.Error("the tab should reset to Details")
	}
	if !m.loading {
		t.Error("switching should start a fresh load")
	}
	if cmd == nil {
		t.Error("switching should fetch the new instance's inventory")
	}
}

func TestSwitchInstancePersistsTheChoice(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m.instance = config.Instance{Name: "prod", URL: rec.server.URL, Token: "1|s"}
	m = withInstances(t, m,
		config.Instance{Name: "prod", URL: rec.server.URL, Token: "1|s"},
		config.Instance{Name: "other", URL: rec.server.URL, Token: "2|s"},
	)

	m = pressDash(t, m, "i")
	m.picker.cursor = 1
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Dashboard)

	if m.cfg.ActiveInstance != "other" {
		t.Errorf("ActiveInstance = %q, want other", m.cfg.ActiveInstance)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ActiveInstance != "other" {
		t.Errorf("saved ActiveInstance = %q, want other to be the new default",
			loaded.ActiveInstance)
	}
}

func TestSwitchInstanceReportsMissingTokenEnv(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m.instance = config.Instance{Name: "prod", URL: rec.server.URL, Token: "1|s"}
	m = withInstances(t, m,
		config.Instance{Name: "prod", URL: rec.server.URL, Token: "1|s"},
		config.Instance{Name: "broken", URL: "http://x", TokenEnv: "DEFINITELY_UNSET_VAR"},
	)

	m = pressDash(t, m, "i")
	m.picker.cursor = 1
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Dashboard)

	// The switch must fail cleanly and stay on the working instance.
	if m.instance.Name != "prod" {
		t.Errorf("instance = %q, want to stay on prod after a failed switch", m.instance.Name)
	}
	if len(m.toasts) == 0 {
		t.Fatal("a failed switch should explain itself")
	}
	msg := m.toasts[len(m.toasts)-1].message
	if !strings.Contains(msg, "DEFINITELY_UNSET_VAR") {
		t.Errorf("toast = %q, want it to name the missing environment variable", msg)
	}
}

func TestSwitchToSameInstanceIsANoop(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m.instance = config.Instance{Name: "prod", URL: rec.server.URL, Token: "1|s"}
	m = withInstances(t, m,
		config.Instance{Name: "prod", URL: rec.server.URL, Token: "1|s"},
		config.Instance{Name: "other", URL: rec.server.URL, Token: "2|s"},
	)
	appsBefore := len(m.inv.Apps)

	m = pressDash(t, m, "i") // cursor lands on prod, the current instance
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Dashboard)

	if cmd != nil {
		t.Error("selecting the current instance should not trigger a reload")
	}
	if len(m.inv.Apps) != appsBefore {
		t.Error("selecting the current instance should not clear the inventory")
	}
	if m.picker.open {
		t.Error("the switcher should close")
	}
}

func TestInstancePickerNavigationClamps(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m = withInstances(t, m,
		config.Instance{Name: "a", URL: "http://a", Token: "1|s"},
		config.Instance{Name: "b", URL: "http://b", Token: "1|s"},
		config.Instance{Name: "c", URL: "http://c", Token: "1|s"},
	)
	m.instance = config.Instance{Name: "a"}
	m = pressDash(t, m, "i")

	m = pressDash(t, m, "up", "up", "up")
	if m.picker.cursor != 0 {
		t.Errorf("cursor = %d, want it clamped at 0", m.picker.cursor)
	}
	m = pressDash(t, m, "down", "down", "down", "down")
	if want := len(m.cfg.Instances) - 1; m.picker.cursor != want {
		t.Errorf("cursor = %d, want it clamped at %d", m.picker.cursor, want)
	}
}

func TestInstancePickerFitsTerminal(t *testing.T) {
	rec := newRecordingServer(t, nil)
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {60, 16}} {
		m := newActionDashboard(t, rec, "web")
		m = withInstances(t, m,
			config.Instance{Name: "production-eu", URL: "https://coolify.a-very-long-domain.example.com", Token: "1|s"},
			config.Instance{Name: "homelab", URL: "http://192.168.1.10:8000", TokenEnv: "HOMELAB_COOLIFY_TOKEN"},
		)
		model, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = model.(Dashboard)
		m = pressDash(t, m, "i")
		assertFitsTerminal(t, m.View(), size.w, size.h)
	}
}
