package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// logsServer serves a container log tail, recording the query it was asked for.
func logsServer(t *testing.T, body func(query string) string) (*recordingServer, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/applications/a1/logs" {
			hits.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"logs": body(r.URL.RawQuery)})
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	return rec, &hits
}

// openLogsTab switches to the Logs tab and runs the resulting fetch.
func openLogsTab(t *testing.T, m Dashboard) Dashboard {
	t.Helper()
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m = model.(Dashboard)
	if m.tab != tabLogs {
		t.Fatalf("tab = %v, want tabLogs", m.tab)
	}
	return applyCmd(t, m, cmd)
}

func TestLogsTabFetchesAndRenders(t *testing.T) {
	rec, _ := logsServer(t, func(string) string {
		return "listening on :3000\nGET / 200 4ms\nGET /health 200 1ms"
	})
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)

	view := m.View()
	for _, want := range []string{"Container logs", "listening on :3000", "GET /health 200 1ms"} {
		if !strings.Contains(view, want) {
			t.Errorf("the logs view should contain %q:\n%s", want, view)
		}
	}
	if !m.logs.follow {
		t.Error("logs should start followed")
	}
}

func TestLogsRequestsTheConfiguredLineCount(t *testing.T) {
	var gotQuery string
	rec, _ := logsServer(t, func(query string) string {
		gotQuery = query
		return "out"
	})
	m := newActionDashboard(t, rec, "web")
	m.cfg.LogLines = 250
	m = openLogsTab(t, m)

	if !strings.Contains(gotQuery, "lines=250") {
		t.Errorf("query = %q, want the configured line count", gotQuery)
	}
}

func TestLogsTimestampsToggleRefetches(t *testing.T) {
	var queries []string
	rec, _ := logsServer(t, func(query string) string {
		queries = append(queries, query)
		if strings.Contains(query, "show_timestamps=true") {
			return "2026-08-14T06:12:00Z listening on :3000"
		}
		return "listening on :3000"
	})
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)

	if strings.Contains(strings.Join(queries, "|"), "show_timestamps") {
		t.Error("timestamps should be off by default")
	}

	// Timestamps are a server-side option, so toggling has to re-fetch.
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = model.(Dashboard)
	if !m.logs.timestamps {
		t.Fatal("t should turn timestamps on")
	}
	if cmd == nil {
		t.Fatal("toggling timestamps should re-fetch the tail")
	}
	m = applyCmd(t, m, cmd)

	if !strings.Contains(strings.Join(queries, "|"), "show_timestamps=true") {
		t.Errorf("queries = %v, want one asking for timestamps", queries)
	}
	if !strings.Contains(m.View(), "2026-08-14T06:12:00Z") {
		t.Error("the timestamped tail should be shown")
	}
	if !strings.Contains(m.View(), "hide timestamps") {
		t.Error("the hint should offer to hide them again")
	}
}

func TestLogsFollowToggle(t *testing.T) {
	rec, _ := logsServer(t, func(string) string { return "one\ntwo\nthree" })
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)

	m = pressDash(t, m, "f")
	if m.logs.follow {
		t.Error("f should turn following off")
	}
	if !strings.Contains(m.View(), "f follow") {
		t.Errorf("the hint should offer to follow again:\n%s", m.View())
	}

	m = pressDash(t, m, "f")
	if !m.logs.follow {
		t.Error("f should turn following back on")
	}
	if !strings.Contains(m.View(), "following") {
		t.Error("the hint should show following is active")
	}
}

func TestLogsScrollingUpStopsFollowing(t *testing.T) {
	lines := make([]string, 0, 300)
	for i := range 300 {
		lines = append(lines, fmt.Sprintf("log line %d", i))
	}
	rec, _ := logsServer(t, func(string) string { return strings.Join(lines, "\n") })
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)

	if !m.logs.follow {
		t.Fatal("should start followed")
	}
	m = pressDash(t, m, "up")
	if m.logs.follow {
		t.Error("scrolling up should stop following, so reading is not interrupted")
	}
	m = pressDash(t, m, "G")
	if !m.logs.follow {
		t.Error("G should resume following")
	}
}

func TestLogsPollStopsWhenTabChanges(t *testing.T) {
	rec, _ := logsServer(t, func(string) string { return "out" })
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)

	// Leaving the tab must end the poll loop rather than keep hitting the API for
	// a pane nobody is looking at.
	m = pressDash(t, m, "1")
	_, cmd := m.Update(logPollMsg{appUUID: "a1"})
	if cmd != nil {
		t.Error("a poll should not re-arm once the Logs tab is closed")
	}
}

func TestLogsPollStopsWhenSelectionChanges(t *testing.T) {
	rec, _ := logsServer(t, func(string) string { return "out" })
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)

	_, cmd := m.Update(logPollMsg{appUUID: "some-other-app"})
	if cmd != nil {
		t.Error("a poll for a different application should be dropped")
	}
}

func TestLogsPollSkipsBeatWhileFetchingButKeepsLoopAlive(t *testing.T) {
	rec, _ := logsServer(t, func(string) string { return "out" })
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)
	m.logs.loading = true

	_, cmd := m.Update(logPollMsg{appUUID: "a1"})
	if cmd == nil {
		t.Error("the poll loop should stay alive even when a beat is skipped")
	}
}

func TestLogsStaleResponseIgnored(t *testing.T) {
	rec, _ := logsServer(t, func(string) string { return "current" })
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)

	model, _ := m.Update(logsMsg{appUUID: "a-different-app", text: "stale output"})
	m = model.(Dashboard)
	if strings.Contains(m.View(), "stale output") {
		t.Error("a response for another application must be discarded")
	}
}

func TestLogsSwitchingApplicationResetsTheTail(t *testing.T) {
	rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/applications/a1/logs":
			_ = json.NewEncoder(w).Encode(map[string]string{"logs": "web output"})
		case "/api/v1/applications/a2/logs":
			_ = json.NewEncoder(w).Encode(map[string]string{"logs": "api output"})
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)
	if !strings.Contains(m.View(), "web output") {
		t.Fatal("web's logs should be shown")
	}

	// Move to another application; its tail must replace the previous one.
	m = pressDash(t, m, "h")
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = applyCmd(t, model.(Dashboard), cmd)

	if strings.Contains(m.View(), "web output") {
		t.Error("the previous application's logs must not linger")
	}
}

func TestLogsEmptyOutputExplainsWhenStopped(t *testing.T) {
	rec, _ := logsServer(t, func(string) string { return "" })
	m := newActionDashboard(t, rec, "worker") // exited:unhealthy in the fixture

	// The worker's UUID is a3, so serve its path too.
	rec.handler = func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/logs") {
			_ = json.NewEncoder(w).Encode(map[string]string{"logs": ""})
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}
	m = openLogsTab(t, m)

	view := m.View()
	// Empty logs for a stopped application is expected, not a fault.
	if !strings.Contains(view, "not running") {
		t.Errorf("empty logs for a stopped app should explain why:\n%s", view)
	}
}

func TestLogsPermissionErrorNamesTheScope(t *testing.T) {
	rec := newRecordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/logs") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Missing permission."}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)

	view := m.View()
	if !strings.Contains(view, "Could not read the container logs") {
		t.Errorf("a failure should explain itself:\n%s", view)
	}
	if !strings.Contains(view, "read:sensitive") {
		t.Error("a 403 should name the permission needed")
	}
}

func TestLogsViewFitsTerminal(t *testing.T) {
	lines := make([]string, 0, 200)
	for i := range 200 {
		lines = append(lines, fmt.Sprintf("%d %s", i, strings.Repeat("wide-log-output ", 14)))
	}
	rec, _ := logsServer(t, func(string) string { return strings.Join(lines, "\n") })

	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}, {60, 16}} {
		m := newActionDashboard(t, rec, "web")
		model, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = model.(Dashboard)
		for i, row := range m.tree.rows {
			if row.kind == rowApp && row.app.Name == "web" {
				m.tree.cursor = i
			}
		}
		m = openLogsTab(t, m)
		assertFitsTerminal(t, m.View(), size.w, size.h)
	}
}

func TestRefreshReloadsTheActiveTab(t *testing.T) {
	rec, hits := logsServer(t, func(string) string { return "out" })
	m := newActionDashboard(t, rec, "web")
	m = openLogsTab(t, m)

	before := hits.Load()
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = model.(Dashboard)
	if cmd == nil {
		t.Fatal("ctrl+r should produce work")
	}
	m = applyCmd(t, m, cmd)

	// ctrl+r should mean "update everything I can see", not just the sidebar.
	if hits.Load() <= before {
		t.Errorf("log hits = %d, want more than %d after ctrl+r", hits.Load(), before)
	}
}

func TestSpinnerStopsWhenIdle(t *testing.T) {
	rec, _ := logsServer(t, func(string) string { return "out" })
	m := newActionDashboard(t, rec, "web")
	m.loading = false

	if m.spinnerNeeded() {
		t.Error("an idle dashboard should not keep the spinner ticking")
	}

	m.logs.loading = true
	if !m.spinnerNeeded() {
		t.Error("a log fetch should keep the spinner ticking")
	}
	m.logs.loading = false

	m.envs.loading = true
	if !m.spinnerNeeded() {
		t.Error("an env fetch should keep the spinner ticking")
	}
}

func TestHardWrapPreservesColumnAlignment(t *testing.T) {
	// Log output is frequently column-aligned; re-flowing on word boundaries
	// would collapse the padding and destroy it.
	line := "GET /api/products        200  18ms"
	if got := hardWrap(line, 80); got != line {
		t.Errorf("hardWrap collapsed whitespace: %q", got)
	}

	// A line longer than the width is cut at exactly the width, not at a word.
	got := hardWrap("abcdefghij", 4)
	want := "abcd\nefgh\nij"
	if got != want {
		t.Errorf("hardWrap = %q, want %q", got, want)
	}

	// Every emitted line must fit.
	long := strings.Repeat("x y ", 60)
	for _, out := range strings.Split(hardWrap(long, 17), "\n") {
		if w := len(out); w > 17 {
			t.Errorf("hardWrap emitted a %d-cell line: %q", w, out)
		}
	}
}

func TestHardWrapHandlesWideRunes(t *testing.T) {
	// Wide characters count as two cells, so a naive rune split would overflow.
	for _, out := range strings.Split(hardWrap(strings.Repeat("日本語", 20), 10), "\n") {
		if w := lipglossWidth(out); w > 10 {
			t.Errorf("hardWrap emitted a %d-cell line with wide runes: %q", w, out)
		}
	}
}

func TestHardWrapZeroWidthIsIdentity(t *testing.T) {
	if got := hardWrap("abc", 0); got != "abc" {
		t.Errorf("hardWrap with width 0 = %q, want the input unchanged", got)
	}
}
