package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// logPollInterval is how often container logs are re-fetched while the Logs tab
// is open. Coolify has no streaming endpoint, so this is a tail poll.
const logPollInterval = 3 * time.Second

// logsMsg carries a fetched log tail.
type logsMsg struct {
	appUUID string
	text    string
	err     error
}

// logPollMsg asks for the log tail to be refreshed.
type logPollMsg struct{ appUUID string }

// logsState is the Logs tab: a polled tail of an application's container logs.
type logsState struct {
	appUUID string
	text    string
	loading bool
	err     error

	// follow pins the view to the newest output.
	follow bool
	// timestamps asks Coolify to prefix each line with its time.
	timestamps bool
	// polling is the application UUID a poll loop is running for, so switching
	// applications cannot leave two loops going.
	polling string
}

// fetchLogs loads the tail of an application's container logs.
func (m Dashboard) fetchLogs(appUUID string, timestamps bool) tea.Cmd {
	client := m.client
	lines := m.cfg.LogLines
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		text, err := client.ApplicationLogs(ctx, appUUID, lines, timestamps)
		return logsMsg{appUUID: appUUID, text: text, err: err}
	}
}

// scheduleLogPoll arms the next log refresh.
func scheduleLogPoll(appUUID string) tea.Cmd {
	return tea.Tick(logPollInterval, func(time.Time) tea.Msg {
		return logPollMsg{appUUID: appUUID}
	})
}

// ensureLogs starts the log tail when the tab is opened or the selection moves.
func (m *Dashboard) ensureLogs() tea.Cmd {
	app, ok := m.tree.selectedApp()
	if !ok {
		return nil
	}
	if m.logs.appUUID == app.UUID && (m.logs.text != "" || m.logs.err != nil || m.logs.loading) {
		return nil
	}

	timestamps := m.logs.timestamps // a display preference, so carry it over
	m.logs = logsState{
		appUUID:    app.UUID,
		loading:    true,
		follow:     true,
		timestamps: timestamps,
		polling:    app.UUID,
	}
	return tea.Batch(m.fetchLogs(app.UUID, timestamps), scheduleLogPoll(app.UUID), m.spin.Tick)
}

// handleLogsMsg folds a fetched log tail into the model.
func (m Dashboard) handleLogsMsg(msg logsMsg) (tea.Model, tea.Cmd) {
	// Drop a response for an application the user has navigated away from.
	if msg.appUUID != m.logs.appUUID {
		return m, nil
	}

	m.logs.loading = false
	m.logs.err = msg.err
	if msg.err == nil {
		m.logs.text = msg.text
	}
	m.syncMain()
	if m.logs.follow {
		m.main.GotoBottom()
	}
	return m, nil
}

// handleLogPoll re-fetches the tail, and re-arms itself.
func (m Dashboard) handleLogPoll(msg logPollMsg) (tea.Model, tea.Cmd) {
	// Stop the loop when the tab is closed or the selection has moved, rather
	// than polling an application nobody is looking at.
	if m.tab != tabLogs || msg.appUUID != m.logs.appUUID {
		if m.logs.polling == msg.appUUID {
			m.logs.polling = ""
		}
		return m, nil
	}
	if m.logs.loading {
		// Skip this beat but keep the loop alive.
		return m, scheduleLogPoll(msg.appUUID)
	}
	m.logs.loading = true
	return m, tea.Batch(
		m.fetchLogs(msg.appUUID, m.logs.timestamps),
		scheduleLogPoll(msg.appUUID),
	)
}

// handleLogsKey routes keys for the Logs tab.
func (m Dashboard) handleLogsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	keys := m.keys
	switch {
	case matches(msg, keys.Follow):
		m.logs.follow = !m.logs.follow
		m.syncMain()
		if m.logs.follow {
			m.main.GotoBottom()
		}
		return m, nil, true

	case matches(msg, keys.Timestamps):
		m.logs.timestamps = !m.logs.timestamps
		if m.logs.appUUID == "" {
			return m, nil, true
		}
		// Timestamps are a server-side option, so the tail has to be re-fetched.
		m.logs.loading = true
		m.syncMain()
		return m, tea.Batch(m.fetchLogs(m.logs.appUUID, m.logs.timestamps), m.spin.Tick), true

	case matches(msg, keys.Bottom):
		m.logs.follow = true
		m.syncMain()
		m.main.GotoBottom()
		return m, nil, true

	case matches(msg, keys.Top):
		m.logs.follow = false
		m.syncMain()
		m.main.GotoTop()
		return m, nil, true

	case matches(msg, keys.Up), matches(msg, keys.PageUp):
		if m.logs.follow {
			m.logs.follow = false
			m.syncMain()
		}
	}
	return m, nil, false
}

// renderLogs renders the container log tail.
func (m Dashboard) renderLogs(app coolify.Application, width int) string {
	s := m.styles
	state := m.logs

	hint := "f follow · t timestamps"
	if state.follow {
		hint = "f unfollow · t timestamps · following"
	}
	if state.timestamps {
		hint = strings.Replace(hint, "t timestamps", "t hide timestamps", 1)
	}

	header := []string{
		"  " + s.Bold.Render("Container logs") + s.Faint.Render("  "+app.Name),
		"  " + s.Faint.Render(hint),
		"",
	}

	if state.err != nil {
		header = append(header,
			"  "+s.Danger.Render("Could not read the container logs"),
			"",
			indent(wrap(state.err.Error(), width-4), "  "))
		if coolify.IsUnauthorized(state.err) {
			header = append(header, "",
				"  "+s.Faint.Render("Container logs need the read:sensitive token permission."))
		}
		return strings.Join(header, "\n")
	}

	if state.loading && state.text == "" {
		header = append(header, "  "+m.spin.View()+s.Muted.Render(" Reading logs…"))
		return strings.Join(header, "\n")
	}

	text := strings.TrimRight(state.text, "\n")
	if strings.TrimSpace(text) == "" {
		note := "No output."
		if !coolify.ParseStatus(app.Status).Running() {
			note = "No output — the application is not running."
		}
		header = append(header, "  "+s.Faint.Render(note))
		return strings.Join(header, "\n")
	}

	avail := max(20, width-4)
	body := make([]string, 0, 64)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			body = append(body, "")
			continue
		}
		// hardWrap, not wrap: log output is often column-aligned, and re-flowing
		// on word boundaries would collapse the padding that aligns it.
		body = append(body, indent(s.Value.Render(hardWrap(line, avail)), "  "))
	}
	return strings.Join(append(header, body...), "\n")
}
