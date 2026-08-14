package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/itsnitinr/coolify-tui/internal/config"
	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// recordingServer captures the requests the dashboard makes, so tests can assert
// that a key produced exactly the intended API call.
type recordingServer struct {
	mu       sync.Mutex
	requests []string
	handler  func(w http.ResponseWriter, r *http.Request)
	server   *httptest.Server
}

func newRecordingServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *recordingServer {
	t.Helper()
	rec := &recordingServer{handler: handler}
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		entry := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			entry += "?" + r.URL.RawQuery
		}
		rec.requests = append(rec.requests, entry)
		rec.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if rec.handler != nil {
			rec.handler(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"message":"Request queued."}`))
	}))
	t.Cleanup(rec.server.Close)
	return rec
}

func (r *recordingServer) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.requests...)
}

// newActionDashboard builds a dashboard wired to rec, with the test inventory
// loaded and the cursor on the named application.
func newActionDashboard(t *testing.T, rec *recordingServer, appName string) Dashboard {
	t.Helper()
	t.Setenv("COOLIFY_TUI_CONFIG_DIR", t.TempDir())

	client, err := coolify.New(rec.server.URL, "1|test")
	if err != nil {
		t.Fatalf("coolify.New: %v", err)
	}
	cfg := config.New()
	m := NewDashboard(cfg, config.Instance{Name: "prod"}, client, NewStyles(DefaultTheme()))

	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	m = model.(Dashboard)

	inv := testInventory()
	inv.FetchedAt = time.Now()
	model, _ = m.Update(inventoryMsg{inv: inv})
	m = model.(Dashboard)

	for i, row := range m.tree.rows {
		if row.kind == rowApp && row.app.Name == appName {
			m.tree.cursor = i
			return m
		}
	}
	t.Fatalf("no application named %q in the test inventory", appName)
	return m
}

func TestActionPromptsForConfirmationByDefault(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")

	m = pressDash(t, m, "d")
	if m.pending == nil {
		t.Fatal("d should open a confirmation prompt")
	}
	if m.pending.kind != actionDeploy {
		t.Errorf("pending kind = %v, want actionDeploy", m.pending.kind)
	}
	if calls := rec.calls(); len(calls) != 0 {
		t.Errorf("no request should be made before confirming, got %v", calls)
	}

	view := m.View()
	for _, want := range []string{"Deploy", "web", "confirm", "cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("the prompt should mention %q:\n%s", want, view)
		}
	}
	// The prompt should say what will happen, not just ask.
	if !strings.Contains(view, "cache") {
		t.Error("the deploy prompt should explain the layer-cache behaviour")
	}
}

func TestActionCancelledLeavesNothingBehind(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")

	m = pressDash(t, m, "d")
	m = pressDash(t, m, "n")
	if m.pending != nil {
		t.Error("n should dismiss the prompt")
	}
	if calls := rec.calls(); len(calls) != 0 {
		t.Errorf("declining should make no request, got %v", calls)
	}

	m = pressDash(t, m, "d")
	m = pressDash(t, m, "esc")
	if m.pending != nil {
		t.Error("esc should dismiss the prompt")
	}
}

func TestConfirmationIsModal(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	cursorBefore := m.tree.cursor

	m = pressDash(t, m, "d")
	// While the prompt is open, navigation and other actions must not fire: a
	// stray key could otherwise move the selection and act on the wrong target.
	m = pressDash(t, m, "down", "down", "2", "/")
	if m.tree.cursor != cursorBefore {
		t.Errorf("cursor moved under the modal: %d -> %d", cursorBefore, m.tree.cursor)
	}
	if m.tab != tabDetails {
		t.Error("tab switching should not work under the modal")
	}
	if m.filtering {
		t.Error("the filter should not open under the modal")
	}
	if m.pending == nil {
		t.Error("the prompt should still be open")
	}
}

func TestActionsIssueTheRightAPICalls(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		appName string
		want    string
	}{
		{"deploy", "d", "web", "POST /api/v1/deploy?uuid=a1"},
		{"force deploy", "D", "web", "POST /api/v1/deploy?force=true&uuid=a1"},
		{"stop running app", "x", "web", "POST /api/v1/applications/a1/stop"},
		{"restart running app", "r", "web", "POST /api/v1/applications/a1/restart"},
		{"start stopped app", "s", "worker", "POST /api/v1/applications/a3/start"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecordingServer(t, nil)
			m := newActionDashboard(t, rec, tc.appName)

			m = pressDash(t, m, tc.key)
			if m.pending == nil {
				t.Fatalf("%s should prompt for confirmation", tc.key)
			}
			// Confirm, and run the resulting command synchronously.
			model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			m = model.(Dashboard)
			if cmd == nil {
				t.Fatal("confirming should produce a command")
			}
			drainCmd(t, cmd)

			calls := rec.calls()
			found := false
			for _, call := range calls {
				if call == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("want a call to %q, got %v", tc.want, calls)
			}
		})
	}
}

func TestActionRejectedWhenStatusMakesItPointless(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		appName string
		wantMsg string
	}{
		{"start a running app", "s", "web", "already running"},
		{"stop a stopped app", "x", "worker", "not running"},
		{"restart a stopped app", "r", "worker", "not running"},
		{"cancel with no deployment", "c", "web", "no deployment running"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecordingServer(t, nil)
			m := newActionDashboard(t, rec, tc.appName)

			m = pressDash(t, m, tc.key)
			if m.pending != nil {
				t.Error("an impossible action should not prompt")
			}
			if len(m.toasts) != 1 {
				t.Fatalf("toasts = %d, want one explaining the refusal", len(m.toasts))
			}
			if !strings.Contains(m.toasts[0].message, tc.wantMsg) {
				t.Errorf("toast = %q, want it to mention %q", m.toasts[0].message, tc.wantMsg)
			}
			if !m.toasts[0].isError {
				t.Error("the refusal should be styled as an error")
			}
			if calls := rec.calls(); len(calls) != 0 {
				t.Errorf("no request should be made, got %v", calls)
			}
		})
	}
}

func TestActionOnServerRowIsRefused(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m.tree.cursor = 0 // a server row

	m = pressDash(t, m, "d")
	if m.pending != nil {
		t.Error("a server row has no application to deploy")
	}
	if len(m.toasts) == 0 || !strings.Contains(m.toasts[0].message, "Select an application") {
		t.Errorf("toasts = %+v, want guidance to select an application", m.toasts)
	}
}

func TestCancelDeploymentUsesTheRunningDeploymentUUID(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")

	inv := testInventory()
	inv.Deployments = []coolify.Deployment{{
		DeploymentUUID:  "dep-42",
		ApplicationName: "web",
		Status:          "in_progress",
		CreatedAt:       time.Now(),
	}}
	model, _ := m.Update(inventoryMsg{inv: inv})
	m = model.(Dashboard)
	for i, row := range m.tree.rows {
		if row.kind == rowApp && row.app.Name == "web" {
			m.tree.cursor = i
		}
	}

	m = pressDash(t, m, "c")
	if m.pending == nil {
		t.Fatal("c should prompt to cancel the build")
	}
	if m.pending.deploymentUUID != "dep-42" {
		t.Errorf("deploymentUUID = %q, want dep-42", m.pending.deploymentUUID)
	}

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Dashboard)
	drainCmd(t, cmd)

	want := "POST /api/v1/deployments/dep-42/cancel"
	found := false
	for _, call := range rec.calls() {
		if call == want {
			found = true
		}
	}
	if !found {
		t.Errorf("want %q, got %v", want, rec.calls())
	}
}

func TestConfirmDisabledSkipsThePrompt(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	off := false
	m.cfg.ConfirmDestructive = &off

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = model.(Dashboard)
	if m.pending != nil {
		t.Error("confirm_destructive: false should skip the prompt")
	}
	if cmd == nil {
		t.Fatal("the action should run immediately")
	}
	drainCmd(t, cmd)

	if len(rec.calls()) == 0 {
		t.Error("the deploy call should have been made")
	}
	if _, busy := m.inFlight["a1"]; !busy {
		t.Error("the application should be marked in-flight")
	}
}

func TestDuplicateActionIsRefusedWhileInFlight(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m.inFlight["a1"] = actionDeploy

	m = pressDash(t, m, "d")
	if m.pending != nil {
		t.Error("a second action should not prompt while one is in flight")
	}
	if len(m.toasts) == 0 || !strings.Contains(m.toasts[0].message, "already deploying") {
		t.Errorf("toasts = %+v, want a note that it is already deploying", m.toasts)
	}
}

func TestActionResultClearsInFlightAndNotifies(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m.inFlight["a1"] = actionDeploy

	model, cmd := m.Update(actionResultMsg{
		kind:    actionDeploy,
		appUUID: "a1",
		appName: "web",
		result:  coolify.ActionResult{Message: "Deployment request queued.", DeploymentUUID: "dep-9"},
	})
	m = model.(Dashboard)

	if _, busy := m.inFlight["a1"]; busy {
		t.Error("the in-flight marker should be cleared")
	}
	if len(m.toasts) != 1 {
		t.Fatalf("toasts = %d, want 1", len(m.toasts))
	}
	if !strings.Contains(m.toasts[0].message, "Deployment request queued") {
		t.Errorf("toast = %q, want Coolify's own message", m.toasts[0].message)
	}
	if m.toasts[0].isError {
		t.Error("a successful action should not be an error toast")
	}
	if cmd == nil {
		t.Error("a successful action should trigger a refresh")
	}
}

func TestActionErrorNamesTheMissingPermission(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m.inFlight["a1"] = actionDeploy

	apiErr := &coolify.APIError{
		StatusCode: http.StatusForbidden,
		Method:     "POST",
		Path:       "/deploy",
		Message:    "Missing permission.",
	}
	model, _ := m.Update(actionResultMsg{
		kind: actionDeploy, appUUID: "a1", appName: "web", err: apiErr,
	})
	m = model.(Dashboard)

	if len(m.toasts) != 1 || !m.toasts[0].isError {
		t.Fatalf("toasts = %+v, want one error toast", m.toasts)
	}
	// Naming the permission is the difference between a dead end and a fix.
	if !strings.Contains(m.toasts[0].message, `"deploy"`) {
		t.Errorf("toast = %q, want it to name the deploy permission", m.toasts[0].message)
	}
}

func TestActionErrorNamesWritePermissionForLifecycle(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")

	apiErr := &coolify.APIError{StatusCode: http.StatusForbidden, Message: "nope"}
	model, _ := m.Update(actionResultMsg{
		kind: actionStop, appUUID: "a1", appName: "web", err: apiErr,
	})
	m = model.(Dashboard)

	if !strings.Contains(m.toasts[0].message, `"write"`) {
		t.Errorf("toast = %q, want it to name the write permission", m.toasts[0].message)
	}
}

func TestActionErrorMentionsRateLimit(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")

	apiErr := &coolify.APIError{StatusCode: http.StatusTooManyRequests, Message: "Too Many Attempts."}
	model, _ := m.Update(actionResultMsg{
		kind: actionDeploy, appUUID: "a1", appName: "web", err: apiErr,
	})
	m = model.(Dashboard)

	if !strings.Contains(m.toasts[0].message, "rate limiting") {
		t.Errorf("toast = %q, want it to mention rate limiting", m.toasts[0].message)
	}
}

func TestInFlightShownInSidebarAndDetails(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")

	// Drive the real path: confirming must re-render the pane straight away,
	// rather than leaving it stale until the next refresh lands.
	m = pressDash(t, m, "r")
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = model.(Dashboard)

	if _, busy := m.inFlight["a1"]; !busy {
		t.Fatal("the application should be marked in-flight")
	}
	if !strings.Contains(m.View(), "restarting") {
		t.Error("the detail pane should say the application is restarting")
	}

	// The sidebar glyph must reflect the action rather than the stale status.
	sidebar := m.tree.view(m.styles, true, m.inFlight)
	if !strings.Contains(sidebar, "◌") {
		t.Errorf("the sidebar should mark the busy application:\n%s", sidebar)
	}
}

func TestRefreshSoonSkippedWhileLoading(t *testing.T) {
	rec := newRecordingServer(t, nil)
	m := newActionDashboard(t, rec, "web")
	m.loading = true

	_, cmd := m.Update(refreshSoonMsg{})
	if cmd != nil {
		t.Error("the follow-up refresh should be skipped while a fetch is outstanding")
	}
}

func TestActionKindMetadataIsComplete(t *testing.T) {
	// Every action needs a verb, a gerund, an explanation and a permission, or
	// the prompt and error paths render blanks.
	for _, kind := range []actionKind{
		actionDeploy, actionForceDeploy, actionStart,
		actionStop, actionRestart, actionCancelDeploy,
	} {
		if kind.verb() == "" || kind.verb() == "Act on" {
			t.Errorf("action %d has no verb", kind)
		}
		if kind.gerund() == "" || kind.gerund() == "working" {
			t.Errorf("action %d has no gerund", kind)
		}
		if kind.consequence() == "" {
			t.Errorf("action %d does not explain itself", kind)
		}
		if kind.permission() == "" {
			t.Errorf("action %d names no permission", kind)
		}
	}
}

func TestDestructiveActionsAreClassified(t *testing.T) {
	destructive := map[actionKind]bool{
		actionStop:         true,
		actionRestart:      true,
		actionCancelDeploy: true,
		actionDeploy:       false,
		actionForceDeploy:  false,
		actionStart:        false,
	}
	for kind, want := range destructive {
		if got := kind.destructive(); got != want {
			t.Errorf("action %d destructive() = %v, want %v", kind, got, want)
		}
	}
}

func TestConfirmModalFitsTheTerminal(t *testing.T) {
	rec := newRecordingServer(t, nil)
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {60, 16}} {
		m := newActionDashboard(t, rec, "web")
		model, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = model.(Dashboard)
		for i, row := range m.tree.rows {
			if row.kind == rowApp && row.app.Name == "web" {
				m.tree.cursor = i
			}
		}
		m = pressDash(t, m, "x") // destructive, so the widest prompt

		assertFitsTerminal(t, m.View(), size.w, size.h)
	}
}

// drainCmd runs a command to completion, discarding its messages, so a test can
// assert on the API calls a keypress produced.
//
// tea.Batch returns a BatchMsg holding the child commands rather than running
// them, so batches are walked recursively — otherwise the HTTP call under test
// would never be made. Tick commands are skipped: they would block for their
// full duration.
func drainCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not complete")
	}

	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child == nil {
				continue
			}
			drainCmdNonBlocking(t, child)
		}
	}
}

// drainCmdNonBlocking runs a batched child command but tolerates one that never
// returns promptly, such as a timer tick.
func drainCmdNonBlocking(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	done := make(chan tea.Msg, 1)
	go func() {
		defer func() { _ = recover() }()
		done <- cmd()
	}()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, child := range batch {
				if child != nil {
					drainCmdNonBlocking(t, child)
				}
			}
		}
	case <-time.After(cmdTimeout):
		// A tick or other long-lived command; not what we are asserting on.
	}
}
