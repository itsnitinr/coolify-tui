package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// deploymentFixture builds a deployment history payload for the fake API.
func deploymentFixture(status string, minutesAgo int, logs string) map[string]any {
	created := time.Now().Add(-time.Duration(minutesAgo) * time.Minute)
	return map[string]any{
		"deployment_uuid":  "dep-" + status + fmt.Sprint(minutesAgo),
		"application_name": "web",
		"status":           status,
		"commit":           "9f2c1ab77e3d4aa",
		"commit_message":   "Bump dependencies",
		"created_at":       created.UTC().Format(time.RFC3339),
		"updated_at":       created.Add(90 * time.Second).UTC().Format(time.RFC3339),
		"is_api":           true,
		"logs":             logs,
	}
}

func buildLogs(entries ...map[string]any) string {
	data, err := json.Marshal(entries)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// deploymentsServer serves a history for a1 plus per-deployment detail.
func deploymentsServer(t *testing.T, history []map[string]any, detail map[string]map[string]any) *recordingServer {
	t.Helper()
	return newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/deployments/applications/a1":
			_ = json.NewEncoder(w).Encode(history)
		case strings.HasPrefix(r.URL.Path, "/api/v1/deployments/"):
			uuid := strings.TrimPrefix(r.URL.Path, "/api/v1/deployments/")
			if dep, ok := detail[uuid]; ok {
				_ = json.NewEncoder(w).Encode(dep)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
}

// openDeploymentsTab selects web and switches to the Deployments tab, running the
// resulting fetch synchronously.
func openDeploymentsTab(t *testing.T, m Dashboard) Dashboard {
	t.Helper()
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	m = model.(Dashboard)
	if m.tab != tabDeployments {
		t.Fatalf("tab = %v, want tabDeployments", m.tab)
	}
	if cmd == nil {
		t.Fatal("opening the Deployments tab should fetch the history")
	}
	return applyCmd(t, m, cmd)
}

// applyCmd runs a command and feeds its message back into the model, following
// batches, so a test can drive an async flow to completion.
func applyCmd(t *testing.T, m Dashboard, cmd tea.Cmd) Dashboard {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := runCmd(t, cmd)
	if msg == nil {
		return m
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child == nil {
				continue
			}
			m = applyCmd(t, m, child)
		}
		return m
	}
	model, next := m.Update(msg)
	m = model.(Dashboard)
	// Follow one level of resulting work so a fetch that triggers a render or a
	// second fetch settles.
	if next != nil {
		if msg := peekCmd(next); msg != nil {
			if _, isTick := msg.(deploymentPollMsg); !isTick {
				model, _ = m.Update(msg)
				m = model.(Dashboard)
			}
		}
	}
	return m
}

// runCmd executes a command with a timeout, returning nil for one that blocks
// (a timer tick).
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	done := make(chan tea.Msg, 1)
	go func() {
		defer func() { _ = recover() }()
		done <- cmd()
	}()
	select {
	case msg := <-done:
		return msg
	case <-time.After(2 * time.Second):
		return nil
	}
}

func peekCmd(cmd tea.Cmd) tea.Msg {
	done := make(chan tea.Msg, 1)
	go func() {
		defer func() { _ = recover() }()
		done <- cmd()
	}()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, child := range batch {
				if child == nil {
					continue
				}
				if inner := peekCmd(child); inner != nil {
					if _, isDetail := inner.(deploymentDetailMsg); isDetail {
						return inner
					}
				}
			}
			return nil
		}
		return msg
	case <-time.After(2 * time.Second):
		return nil
	}
}

func TestDeploymentsTabLoadsHistory(t *testing.T) {
	history := []map[string]any{
		deploymentFixture("finished", 5, ""),
		deploymentFixture("failed", 60, ""),
	}
	rec := deploymentsServer(t, history, nil)
	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)

	if len(m.deployments.list) != 2 {
		t.Fatalf("history = %d entries, want 2", len(m.deployments.list))
	}
	if m.deployments.appUUID != "a1" {
		t.Errorf("appUUID = %q, want a1", m.deployments.appUUID)
	}

	view := m.View()
	for _, want := range []string{"finished", "failed", "9f2c1ab", "api"} {
		if !strings.Contains(view, want) {
			t.Errorf("the history view should show %q:\n%s", want, view)
		}
	}
}

func TestDeploymentsHistoryEmptyState(t *testing.T) {
	rec := deploymentsServer(t, []map[string]any{}, nil)
	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)

	view := m.View()
	if !strings.Contains(view, "No deployments recorded") {
		t.Errorf("an empty history should say so:\n%s", view)
	}
	if !strings.Contains(view, "d to deploy") {
		t.Error("the empty state should point at the deploy key")
	}
}

func TestDeploymentsHistoryErrorNamesPermission(t *testing.T) {
	rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/deployments/applications/") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Missing permission."}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)

	view := m.View()
	if !strings.Contains(view, "Could not load") {
		t.Errorf("a failed history load should explain itself:\n%s", view)
	}
	if !strings.Contains(view, "read permission") {
		t.Error("a 403 should name the permission needed")
	}
}

func TestDeploymentsSwitchingAppInvalidatesTheTab(t *testing.T) {
	rec := deploymentsServer(t, []map[string]any{deploymentFixture("finished", 5, "")}, nil)
	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)
	if len(m.deployments.list) == 0 {
		t.Fatal("history should be loaded")
	}

	// Moving to a different application must not leave the previous
	// application's history on screen.
	m = pressDash(t, m, "h") // focus sidebar
	m = pressDash(t, m, "down")

	if m.deployments.appUUID == "a1" {
		t.Error("the tab should be re-scoped to the newly selected application")
	}
	if m.deployments.showingLogs() {
		t.Error("an open log viewer belongs to the old application and must close")
	}
}

func TestDeploymentsStaleResponseIsIgnored(t *testing.T) {
	rec := deploymentsServer(t, []map[string]any{deploymentFixture("finished", 5, "")}, nil)
	m := newActionDashboard(t, rec, "web")
	m.deployments = deploymentsState{appUUID: "a4"} // the user has moved on

	model, _ := m.Update(deploymentsMsg{
		appUUID: "a1",
		list:    []coolify.Deployment{{DeploymentUUID: "old"}},
	})
	m = model.(Dashboard)

	if len(m.deployments.list) != 0 {
		t.Error("a response for a different application must be discarded")
	}
}

func TestDeploymentsAutoOpensRunningBuild(t *testing.T) {
	running := deploymentFixture("in_progress", 1, "")
	history := []map[string]any{running, deploymentFixture("finished", 30, "")}
	detail := map[string]map[string]any{
		running["deployment_uuid"].(string): running,
	}
	rec := deploymentsServer(t, history, detail)
	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)

	// A build in progress is what the user came to see, so the log viewer should
	// already be open on it.
	if !m.deployments.showingLogs() {
		t.Fatal("a running build should open its log viewer automatically")
	}
	if m.deployments.openUUID != running["deployment_uuid"].(string) {
		t.Errorf("openUUID = %q, want the running build", m.deployments.openUUID)
	}
	if !m.deployments.follow {
		t.Error("a running build should be followed by default")
	}
}

func TestDeploymentsDoesNotAutoOpenFinishedBuilds(t *testing.T) {
	history := []map[string]any{
		deploymentFixture("finished", 5, ""),
		deploymentFixture("failed", 60, ""),
	}
	rec := deploymentsServer(t, history, nil)
	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)

	if m.deployments.showingLogs() {
		t.Error("a settled history should stay on the list, not jump into a log")
	}
}

func TestDeploymentLogsRenderCommandsAndStderr(t *testing.T) {
	logs := buildLogs(
		map[string]any{"command": "npm ci", "type": "stdout"},
		map[string]any{"output": "added 412 packages", "type": "stdout"},
		map[string]any{"output": "npm warn deprecated", "type": "stderr"},
		map[string]any{"output": "SECRET_TOKEN=hidden", "type": "stdout", "hidden": true},
	)
	dep := deploymentFixture("finished", 5, logs)
	uuid := dep["deployment_uuid"].(string)
	rec := deploymentsServer(t, []map[string]any{dep}, map[string]map[string]any{uuid: dep})

	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Dashboard)
	if !m.deployments.showingLogs() {
		t.Fatal("enter should open the log viewer")
	}
	m = applyCmd(t, m, cmd)

	view := m.View()
	if !strings.Contains(view, "$ npm ci") {
		t.Errorf("a command entry should render as a shell prompt:\n%s", view)
	}
	if !strings.Contains(view, "added 412 packages") {
		t.Error("stdout output should be shown")
	}
	if !strings.Contains(view, "npm warn deprecated") {
		t.Error("stderr output should be shown")
	}
	// Coolify marks entries it does not want surfaced; honour that.
	if strings.Contains(view, "SECRET_TOKEN") {
		t.Error("hidden log entries must not be rendered")
	}
}

func TestDeploymentLogsEscReturnsToHistory(t *testing.T) {
	dep := deploymentFixture("finished", 5, buildLogs(map[string]any{"output": "done"}))
	uuid := dep["deployment_uuid"].(string)
	rec := deploymentsServer(t, []map[string]any{dep}, map[string]map[string]any{uuid: dep})

	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = applyCmd(t, model.(Dashboard), cmd)

	m = pressDash(t, m, "esc")
	if m.deployments.showingLogs() {
		t.Error("esc should return to the history list")
	}
	if m.focus != paneMain {
		t.Error("esc out of the log viewer should stay in the detail pane")
	}
	if !strings.Contains(m.View(), "Deployments") {
		t.Error("the history list should be showing again")
	}
}

func TestDeploymentLogsFollowToggle(t *testing.T) {
	dep := deploymentFixture("in_progress", 1, buildLogs(map[string]any{"output": "building"}))
	uuid := dep["deployment_uuid"].(string)
	rec := deploymentsServer(t, []map[string]any{dep}, map[string]map[string]any{uuid: dep})

	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)
	if !m.deployments.follow {
		t.Fatal("a running build should start followed")
	}

	m = pressDash(t, m, "f")
	if m.deployments.follow {
		t.Error("f should turn following off")
	}
	if !strings.Contains(m.View(), "f follow") {
		t.Error("the hint should offer to follow again")
	}

	m = pressDash(t, m, "f")
	if !m.deployments.follow {
		t.Error("f should turn following back on")
	}
	if !strings.Contains(m.View(), "following") {
		t.Error("the hint should show that following is active")
	}
}

func TestDeploymentLogsScrollingUpStopsFollowing(t *testing.T) {
	// Enough output to make the pane scrollable.
	entries := make([]map[string]any, 0, 200)
	for i := range 200 {
		entries = append(entries, map[string]any{"output": fmt.Sprintf("line %d", i)})
	}
	dep := deploymentFixture("in_progress", 1, buildLogs(entries...))
	uuid := dep["deployment_uuid"].(string)
	rec := deploymentsServer(t, []map[string]any{dep}, map[string]map[string]any{uuid: dep})

	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)
	if !m.deployments.follow {
		t.Fatal("should start followed")
	}

	// Scrolling back to read something must not fight the incoming output.
	m = pressDash(t, m, "up")
	if m.deployments.follow {
		t.Error("scrolling up should stop following")
	}

	// G returns to the tail and resumes following.
	m = pressDash(t, m, "G")
	if !m.deployments.follow {
		t.Error("G should resume following")
	}
}

func TestDeploymentPollIgnoredForClosedViewer(t *testing.T) {
	rec := deploymentsServer(t, []map[string]any{deploymentFixture("finished", 5, "")}, nil)
	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)

	_, cmd := m.Update(deploymentPollMsg{uuid: "dep-somethingelse"})
	if cmd != nil {
		t.Error("a poll for a deployment that is not open should be dropped")
	}
}

func TestDeploymentPollSkippedWhileFetching(t *testing.T) {
	dep := deploymentFixture("in_progress", 1, "")
	uuid := dep["deployment_uuid"].(string)
	rec := deploymentsServer(t, []map[string]any{dep}, map[string]map[string]any{uuid: dep})

	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)
	m.deployments.detailBusy = true

	_, cmd := m.Update(deploymentPollMsg{uuid: m.deployments.openUUID})
	if cmd != nil {
		t.Error("a poll should not stack a second request on an outstanding one")
	}
}

func TestDeploymentFinishingNotifiesAndRefreshes(t *testing.T) {
	dep := deploymentFixture("in_progress", 1, "")
	uuid := dep["deployment_uuid"].(string)
	rec := deploymentsServer(t, []map[string]any{dep}, map[string]map[string]any{uuid: dep})

	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)
	if !m.deployments.detail.InProgress() {
		m.deployments.detail.Status = "in_progress"
	}

	// The build completes.
	finished := m.deployments.detail
	finished.Status = "failed"
	finished.UpdatedAt = time.Now()
	model, cmd := m.Update(deploymentDetailMsg{uuid: m.deployments.openUUID, dep: finished})
	m = model.(Dashboard)

	if m.deployments.polling != "" {
		t.Error("polling should stop once the build settles")
	}
	if cmd == nil {
		t.Fatal("finishing should trigger a notification and refresh")
	}
	if len(m.toasts) == 0 {
		// The toast is queued via the returned batch; run it.
		m = applyCmd(t, m, cmd)
	}

	found := false
	for _, toast := range m.toasts {
		if strings.Contains(toast.message, "failed") {
			found = true
			if !toast.isError {
				t.Error("a failed build should be an error toast")
			}
		}
	}
	if !found {
		t.Errorf("toasts = %+v, want one reporting the failure", m.toasts)
	}
}

func TestDeploymentDetailStatusSyncsIntoHistory(t *testing.T) {
	dep := deploymentFixture("in_progress", 1, "")
	uuid := dep["deployment_uuid"].(string)
	rec := deploymentsServer(t, []map[string]any{dep}, map[string]map[string]any{uuid: dep})

	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)

	updated := m.deployments.detail
	updated.Status = "finished"
	model, _ := m.Update(deploymentDetailMsg{uuid: m.deployments.openUUID, dep: updated})
	m = model.(Dashboard)

	for _, entry := range m.deployments.list {
		if entry.DeploymentUUID == uuid && entry.Status != "finished" {
			t.Errorf("history row status = %q, want it synced to finished", entry.Status)
		}
	}
}

func TestDeploymentDetailStaleResponseIgnored(t *testing.T) {
	dep := deploymentFixture("finished", 5, "")
	uuid := dep["deployment_uuid"].(string)
	rec := deploymentsServer(t, []map[string]any{dep}, map[string]map[string]any{uuid: dep})

	m := newActionDashboard(t, rec, "web")
	m = openDeploymentsTab(t, m)
	m.deployments.openUUID = "dep-current"
	m.deployments.detail = coolify.Deployment{DeploymentUUID: "dep-current", Status: "queued"}

	model, _ := m.Update(deploymentDetailMsg{
		uuid: "dep-old",
		dep:  coolify.Deployment{DeploymentUUID: "dep-old", Status: "finished"},
	})
	m = model.(Dashboard)

	if m.deployments.detail.DeploymentUUID != "dep-current" {
		t.Error("a response for a closed deployment must not replace the open one")
	}
}

func TestDeployActionOpensTheBuildLog(t *testing.T) {
	dep := deploymentFixture("in_progress", 0, "")
	uuid := dep["deployment_uuid"].(string)
	rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/deploy":
			_, _ = w.Write([]byte(`{"deployments":[{"message":"Deployment request queued.",` +
				`"resource_uuid":"a1","deployment_uuid":"` + uuid + `"}]}`))
		case r.URL.Path == "/api/v1/deployments/applications/a1":
			_ = json.NewEncoder(w).Encode([]map[string]any{dep})
		case r.URL.Path == "/api/v1/deployments/"+uuid:
			_ = json.NewEncoder(w).Encode(dep)
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})

	m := newActionDashboard(t, rec, "web")
	m = pressDash(t, m, "d")
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = applyCmd(t, model.(Dashboard), cmd)

	// Triggering a deploy should land the user on the build they just started.
	if m.tab != tabDeployments {
		t.Errorf("tab = %v, want the Deployments tab after a deploy", m.tab)
	}
}

func TestDeploymentsViewFitsTerminal(t *testing.T) {
	entries := make([]map[string]any, 0, 60)
	for i := range 60 {
		entries = append(entries, map[string]any{
			"output": strings.Repeat(fmt.Sprintf("output-%d ", i), 12),
		})
	}
	dep := deploymentFixture("in_progress", 1, buildLogs(entries...))
	uuid := dep["deployment_uuid"].(string)

	history := make([]map[string]any, 0, 25)
	for i := range 25 {
		history = append(history, deploymentFixture("finished", i+2, ""))
	}
	history = append([]map[string]any{dep}, history...)

	rec := deploymentsServer(t, history, map[string]map[string]any{uuid: dep})

	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {60, 16}} {
		m := newActionDashboard(t, rec, "web")
		model, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = model.(Dashboard)
		for i, row := range m.tree.rows {
			if row.kind == rowApp && row.app.Name == "web" {
				m.tree.cursor = i
			}
		}
		m = openDeploymentsTab(t, m)

		// Log viewer (auto-opened for the running build).
		assertFitsTerminal(t, m.View(), size.w, size.h)

		// History list.
		m = pressDash(t, m, "esc")
		assertFitsTerminal(t, m.View(), size.w, size.h)
	}
}

func TestIsFailedStatus(t *testing.T) {
	for status, want := range map[string]bool{
		"failed":            true,
		"error":             true,
		"cancelled_by_user": true,
		"cancelled":         true,
		"finished":          false,
		"in_progress":       false,
		"queued":            false,
	} {
		if got := isFailedStatus(status); got != want {
			t.Errorf("isFailedStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestPlainDeploymentGlyphCoversStatuses(t *testing.T) {
	for _, status := range []string{
		"finished", "failed", "in_progress", "queued", "cancelled_by_user", "weird",
	} {
		if plainDeploymentGlyph(status) == "" {
			t.Errorf("no glyph for status %q", status)
		}
	}
}

func TestDisplayDeploymentStatusShortensLongNames(t *testing.T) {
	// The history column is 11 cells; anything longer would be truncated with an
	// ellipsis, which reads worse than a shorter synonym.
	for status, want := range map[string]string{
		"cancelled_by_user": "cancelled",
		"in_progress":       "building",
		"finished":          "finished",
		"failed":            "failed",
		"queued":            "queued",
	} {
		got := displayDeploymentStatus(status)
		if got != want {
			t.Errorf("displayDeploymentStatus(%q) = %q, want %q", status, got, want)
		}
		if len(got) > 11 {
			t.Errorf("displayDeploymentStatus(%q) = %q, too long for the column", status, got)
		}
	}
}
