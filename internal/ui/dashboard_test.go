package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/itsnitinr/coolify-tui/internal/config"
	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// newTestDashboard builds a dashboard preloaded with the test inventory at the
// given terminal size.
func newTestDashboard(t *testing.T, width, height int) Dashboard {
	t.Helper()
	t.Setenv("COOLIFY_TUI_CONFIG_DIR", t.TempDir())

	client, err := coolify.New("coolify.example.com", "1|test")
	if err != nil {
		t.Fatalf("coolify.New: %v", err)
	}
	cfg := config.New()
	inst := config.Instance{Name: "prod", URL: "coolify.example.com", Token: "1|test"}

	m := NewDashboard(cfg, inst, client, NewStyles(DefaultTheme()))
	model, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = model.(Dashboard)

	inv := testInventory()
	inv.FetchedAt = time.Now()
	model, _ = m.Update(inventoryMsg{inv: inv})
	return model.(Dashboard)
}

func pressDash(t *testing.T, m Dashboard, keys ...string) Dashboard {
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
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		case "ctrl+r":
			msg = tea.KeyMsg{Type: tea.KeyCtrlR}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		model, _ := m.Update(msg)
		m = model.(Dashboard)
	}
	return m
}

// TestDashboardViewFillsExactlyTheTerminal is the regression test for panel
// height arithmetic. The two panels plus the header and help bar must add up to
// the terminal height exactly, and no line may exceed its width, or the terminal
// will scroll and the layout will tear.
func TestDashboardViewFillsExactlyTheTerminal(t *testing.T) {
	sizes := []struct{ w, h int }{
		{80, 24},
		{100, 30},
		{120, 34},
		{200, 50},
		{60, 14},
		{61, 15},
	}
	for _, size := range sizes {
		m := newTestDashboard(t, size.w, size.h)
		view := m.View()

		if got := lipgloss.Height(view); got != size.h {
			t.Errorf("%dx%d: view is %d lines tall, want %d", size.w, size.h, got, size.h)
		}
		for i, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > size.w {
				t.Errorf("%dx%d: line %d is %d cells wide, want <= %d:\n%q",
					size.w, size.h, i, got, size.w, line)
			}
		}
	}
}

func TestDashboardViewFillsTerminalWithFilterBar(t *testing.T) {
	m := newTestDashboard(t, 100, 30)
	m = pressDash(t, m, "/", "w", "e")

	view := m.View()
	if got := lipgloss.Height(view); got != 30 {
		t.Errorf("with the filter bar open the view is %d lines, want 30", got)
	}
}

func TestDashboardTooSmallTerminalDegradesGracefully(t *testing.T) {
	m := newTestDashboard(t, 30, 8)
	view := m.View()
	if !strings.Contains(view, "too small") {
		t.Errorf("a tiny terminal should explain itself, got:\n%s", view)
	}
}

func TestDashboardNavigationMovesSelectionAndDetail(t *testing.T) {
	m := newTestDashboard(t, 120, 34)

	// Row 0 is the alpha server row.
	row, ok := m.tree.selected()
	if !ok || row.kind != rowServer {
		t.Fatalf("initial selection = %+v, want a server row", row)
	}
	if !strings.Contains(m.View(), "alpha") {
		t.Error("the detail pane should show the selected server")
	}

	// Moving down lands on an application, and the detail pane follows.
	m = pressDash(t, m, "down")
	app, ok := m.tree.selectedApp()
	if !ok {
		t.Fatal("expected an application to be selected")
	}
	if !strings.Contains(m.View(), app.Name) {
		t.Errorf("detail pane does not show %q", app.Name)
	}
}

func TestDashboardTabSwitching(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	m = pressDash(t, m, "down") // select an application

	for _, tc := range []struct {
		key  string
		want tab
	}{
		{"2", tabLogs},
		{"3", tabDeployments},
		{"4", tabEnv},
		{"1", tabDetails},
	} {
		m = pressDash(t, m, tc.key)
		if m.tab != tc.want {
			t.Errorf("after %q: tab = %v, want %v", tc.key, m.tab, tc.want)
		}
	}

	// Bracket keys cycle, and wrap.
	m = pressDash(t, m, "[")
	if m.tab != tabEnv {
		t.Errorf("[ from Details should wrap to Env, got %v", m.tab)
	}
	m = pressDash(t, m, "]")
	if m.tab != tabDetails {
		t.Errorf("] from Env should wrap to Details, got %v", m.tab)
	}
}

func TestDashboardPaneFocusToggles(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	if m.focus != paneSidebar {
		t.Fatalf("initial focus = %v, want the sidebar", m.focus)
	}
	m = pressDash(t, m, "tab")
	if m.focus != paneMain {
		t.Errorf("after tab: focus = %v, want the detail pane", m.focus)
	}
	m = pressDash(t, m, "tab")
	if m.focus != paneSidebar {
		t.Errorf("tab should toggle back to the sidebar, got %v", m.focus)
	}

	m = pressDash(t, m, "l")
	if m.focus != paneMain {
		t.Errorf("l should focus the detail pane, got %v", m.focus)
	}
	m = pressDash(t, m, "esc")
	if m.focus != paneSidebar {
		t.Errorf("esc from the detail pane should return to the sidebar, got %v", m.focus)
	}
}

func TestDashboardEnterOnServerFoldsAndOnAppFocusesDetail(t *testing.T) {
	m := newTestDashboard(t, 120, 34)

	before := len(m.tree.rows)
	m = pressDash(t, m, "enter")
	if len(m.tree.rows) >= before {
		t.Error("enter on a server row should fold it")
	}
	if m.focus != paneSidebar {
		t.Error("folding should not move focus")
	}

	m = pressDash(t, m, "enter") // unfold
	m = pressDash(t, m, "down", "enter")
	if m.focus != paneMain {
		t.Errorf("enter on an application should focus its detail pane, got %v", m.focus)
	}
}

func TestDashboardFilterFlow(t *testing.T) {
	m := newTestDashboard(t, 120, 34)

	m = pressDash(t, m, "/")
	if !m.filtering {
		t.Fatal("/ should open the filter prompt")
	}
	m = pressDash(t, m, "d", "o", "c")
	if m.tree.filter != "doc" {
		t.Errorf("filter = %q, want doc", m.tree.filter)
	}
	// Only the beta server and docs should remain.
	if len(m.tree.rows) != 2 {
		t.Errorf("rows = %d, want 2 (beta + docs)", len(m.tree.rows))
	}

	// Enter keeps the filter but leaves the prompt.
	m = pressDash(t, m, "enter")
	if m.filtering {
		t.Error("enter should close the filter prompt")
	}
	if m.tree.filter != "doc" {
		t.Error("enter should keep the filter applied")
	}

	// Esc from the tree clears it.
	m = pressDash(t, m, "/")
	m = pressDash(t, m, "esc")
	if m.filtering || m.tree.filter != "" {
		t.Errorf("esc should abandon the filter, got filtering=%v filter=%q", m.filtering, m.tree.filter)
	}
	if len(m.tree.rows) != len(newTestDashboard(t, 120, 34).tree.rows) {
		t.Error("clearing the filter should restore every row")
	}
}

func TestDashboardFilterKeysDoNotTriggerActions(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	m = pressDash(t, m, "/")

	// "q" would quit and "d" would deploy outside the prompt; inside it they are
	// just characters.
	m = pressDash(t, m, "q", "d")
	if m.tree.filter != "qd" {
		t.Errorf("filter = %q, want the typed characters", m.tree.filter)
	}
	if m.showHelp {
		t.Error("typing in the filter should not open help")
	}
}

func TestDashboardHelpOverlayTogglesAndListsWarnings(t *testing.T) {
	m := newTestDashboard(t, 120, 40)

	inv := testInventory()
	inv.Warnings = []string{"server homelab: 500 server unreachable over ssh"}
	model, _ := m.Update(inventoryMsg{inv: inv})
	m = model.(Dashboard)

	m = pressDash(t, m, "?")
	if !m.showHelp {
		t.Fatal("? should open the help overlay")
	}
	view := m.View()
	if !strings.Contains(view, "keybindings") {
		t.Error("the overlay should list keybindings")
	}
	if !strings.Contains(view, "unreachable over ssh") {
		t.Error("the overlay should show refresh warnings; they are counted nowhere else")
	}

	m = pressDash(t, m, "esc")
	if m.showHelp {
		t.Error("esc should close the overlay")
	}
}

func TestDashboardKeepsStaleInventoryOnRefreshFailure(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	appsBefore := len(m.inv.Apps)

	model, _ := m.Update(inventoryMsg{err: errString("connection refused")})
	m = model.(Dashboard)

	if len(m.inv.Apps) != appsBefore {
		t.Errorf("apps = %d after a failed refresh, want the previous %d retained",
			len(m.inv.Apps), appsBefore)
	}
	if m.loadErr == nil {
		t.Error("the error should be recorded")
	}
	if len(m.toasts) != 1 {
		t.Errorf("toasts = %d, want one notifying the failure", len(m.toasts))
	}
	if !strings.Contains(m.View(), "stale") {
		t.Error("the header should mark the data as stale")
	}
}

func TestDashboardFirstLoadFailureExplainsItself(t *testing.T) {
	t.Setenv("COOLIFY_TUI_CONFIG_DIR", t.TempDir())
	client, err := coolify.New("coolify.example.com", "1|test")
	if err != nil {
		t.Fatal(err)
	}
	m := NewDashboard(config.New(), config.Instance{Name: "prod"}, client, NewStyles(DefaultTheme()))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = model.(Dashboard).Update(inventoryMsg{err: errString("dial tcp: connection refused")})
	m = model.(Dashboard)

	view := m.View()
	if !strings.Contains(view, "Could not load") {
		t.Errorf("a failed first load should explain itself:\n%s", view)
	}
	if !strings.Contains(view, "connection refused") {
		t.Error("the underlying error should be shown")
	}
}

func TestDashboardRefreshSkippedWhileLoading(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	m.loading = true

	// A tick while a fetch is outstanding must re-arm the timer without issuing
	// a second request; Coolify rate-limits at 200 requests/minute.
	model, cmd := m.Update(tickMsg(time.Now()))
	m = model.(Dashboard)
	if cmd == nil {
		t.Fatal("the refresh timer should be re-armed")
	}
	if !m.loading {
		t.Error("loading should stay set")
	}
}

func TestDashboardDeployingAppShownInSidebarAndHeader(t *testing.T) {
	m := newTestDashboard(t, 120, 34)

	inv := testInventory()
	inv.Deployments = []coolify.Deployment{{
		DeploymentUUID:  "dep-1",
		ApplicationName: "docs",
		Status:          "in_progress",
		CreatedAt:       time.Now().Add(-30 * time.Second),
	}}
	model, _ := m.Update(inventoryMsg{inv: inv})
	m = model.(Dashboard)

	if !strings.Contains(m.View(), "1 deploying") {
		t.Error("the header should count in-flight deployments")
	}

	for _, row := range m.tree.rows {
		if row.kind == rowApp && row.app.Name == "docs" {
			if !row.deploying {
				t.Error("the docs row should be marked as deploying")
			}
			return
		}
	}
	t.Fatal("no row for docs")
}

func TestDashboardQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		m := newTestDashboard(t, 120, 34)
		var msg tea.KeyMsg
		if k == "q" {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		}
		_, cmd := m.Update(msg)
		if cmd == nil {
			t.Errorf("%s should quit", k)
		}
	}
}

func TestDashboardCtrlCQuitsFromHelpOverlay(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	m = pressDash(t, m, "?")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("ctrl+c should quit even with the help overlay open")
	}
}

func TestDashboardToastExpiry(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	_ = m.notify("first", false)
	_ = m.notify("second", true)
	if len(m.toasts) != 2 {
		t.Fatalf("toasts = %d, want 2", len(m.toasts))
	}

	view := m.View()
	if !strings.Contains(view, "first") || !strings.Contains(view, "second") {
		t.Error("both toasts should be visible")
	}
	// The overlay must not change the screen's dimensions.
	if got := lipgloss.Height(view); got != 34 {
		t.Errorf("toasts changed the view height to %d, want 34", got)
	}

	model, _ := m.Update(toastExpiredMsg{id: m.toasts[0].id})
	m = model.(Dashboard)
	if len(m.toasts) != 1 || m.toasts[0].message != "second" {
		t.Errorf("toasts after expiry = %+v, want only the second", m.toasts)
	}
}

func TestOverlayRightKeepsWidth(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	base := strings.Repeat(" ", 40)
	overlay := styles.Toast.Render("done")
	got := overlayRight(base, overlay, 40)
	if w := lipgloss.Width(got); w != 40 {
		t.Errorf("overlayRight width = %d, want 40", w)
	}
}

func TestOpenURLRejectsNonHTTPSchemes(t *testing.T) {
	// An application's fqdn is remote input; a non-http scheme must never reach
	// a command line.
	for _, raw := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"ssh://host",
		"",
		"https://",
	} {
		if err := openURL(raw); err == nil {
			t.Errorf("openURL(%q) = nil, want an error", raw)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{-time.Second, "0s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m30s"},
		{3*time.Minute + 5*time.Second, "3m05s"},
		{time.Hour + 4*time.Minute, "1h04m"},
	}
	for _, tc := range tests {
		if got := formatDuration(tc.in); got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatRelative(t *testing.T) {
	now := time.Now()
	tests := []struct {
		in   time.Time
		want string
	}{
		{time.Time{}, ""},
		{now.Add(-3 * time.Second), "just now"},
		{now.Add(-30 * time.Second), "30s ago"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-50 * time.Hour), "2d ago"},
	}
	for _, tc := range tests {
		if got := formatRelative(tc.in); got != tc.want {
			t.Errorf("formatRelative(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Anything older than a week falls back to a date.
	old := now.Add(-30 * 24 * time.Hour)
	if got := formatRelative(old); got != old.Local().Format("2006-01-02") {
		t.Errorf("formatRelative for an old time = %q, want a date", got)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("one\ntwo"); got != "one" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("only"); got != "only" {
		t.Errorf("firstLine = %q", got)
	}
}

func TestClamp(t *testing.T) {
	if got := clamp(5, 10, 20); got != 10 {
		t.Errorf("clamp(5,10,20) = %d", got)
	}
	if got := clamp(25, 10, 20); got != 20 {
		t.Errorf("clamp(25,10,20) = %d", got)
	}
	if got := clamp(15, 10, 20); got != 15 {
		t.Errorf("clamp(15,10,20) = %d", got)
	}
}

func TestAppDetailsRendersKeyFacts(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	app := coolify.Application{
		UUID:          "app-1",
		Name:          "storefront",
		Status:        "running:unhealthy",
		FQDN:          "https://shop.example.com,https://www.shop.example.com",
		GitRepository: "https://github.com/acme/storefront",
		GitBranch:     "main",
		GitCommitSHA:  "9f2c1ab77e3d4aa",
		BuildPack:     "nixpacks",
		PortsExposes:  "3000",
		ServerName:    "alpha",
		ServerUUID:    "srv-a",
	}
	out := m.renderAppDetails(app, 80)

	for _, want := range []string{
		"storefront", "running:unhealthy", "alpha",
		"shop.example.com", "www.shop.example.com",
		"main", "9f2c1ab", "nixpacks", "3000", "app-1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("app details missing %q", want)
		}
	}
	// The full SHA should be abbreviated, not dumped.
	if strings.Contains(out, "9f2c1ab77e3d4aa") {
		t.Error("the commit SHA should be shortened")
	}
}

func TestAppDetailsShowsInFlightDeployment(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	inv := testInventory()
	inv.Deployments = []coolify.Deployment{{
		DeploymentUUID:  "dep-1",
		ApplicationName: "docs",
		Status:          "in_progress",
		Commit:          "abcdef1234",
		CreatedAt:       time.Now().Add(-45 * time.Second),
	}}
	model, _ := m.Update(inventoryMsg{inv: inv})
	m = model.(Dashboard)

	out := m.renderAppDetails(coolify.Application{Name: "docs", Status: "running:healthy"}, 80)
	if !strings.Contains(out, "deploying") {
		t.Error("an in-flight deployment should be called out on the details pane")
	}
	if !strings.Contains(out, "abcdef1") {
		t.Error("the deploying commit should be shown")
	}
}

func TestServerDetailsRendersHealthAndRollup(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	srv := coolify.Server{
		UUID: "srv-a", Name: "alpha", IP: "10.0.0.1", Port: 22, User: "root",
		ProxyType: "traefik",
		Settings: coolify.ServerSettings{
			IsReachable: true, IsUsable: true, ConcurrentBuilds: 2,
			WildcardDomain: "*.apps.example.com",
		},
	}
	out := m.renderServerDetails(srv, 80)
	for _, want := range []string{
		"alpha", "healthy", "10.0.0.1:22", "root", "traefik",
		"*.apps.example.com", "Applications (3)", "1 running",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("server details missing %q", want)
		}
	}
}

func TestServerDetailsExplainsSyntheticRow(t *testing.T) {
	m := newTestDashboard(t, 120, 34)
	out := m.renderServerDetails(coolify.Server{Name: "unknown server", UUID: ""}, 80)
	if !strings.Contains(out, "could not be matched") {
		t.Errorf("the synthetic row should explain itself:\n%s", out)
	}
	// It must not claim the server is unreachable, which would be a lie.
	if strings.Contains(out, "unreachable") {
		t.Error("the synthetic row must not be described as unreachable")
	}
}
