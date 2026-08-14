package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// deploymentHistoryLimit is how many past deployments to request. Enough to see
// a pattern of failures without paging.
const deploymentHistoryLimit = 25

// deploymentPollInterval is how often a running build's logs are re-fetched.
// Faster than the dashboard's refresh interval, because a build in progress is
// what the user is actively watching.
const deploymentPollInterval = 2 * time.Second

// deploymentsMsg carries a fetched deployment history.
type deploymentsMsg struct {
	appUUID string
	list    []coolify.Deployment
	err     error
}

// deploymentDetailMsg carries a single deployment including its build logs.
type deploymentDetailMsg struct {
	uuid string
	dep  coolify.Deployment
	err  error
}

// deploymentPollMsg asks for a running deployment's logs to be refreshed.
type deploymentPollMsg struct{ uuid string }

// deploymentsState is the Deployments tab: a history list, and a log viewer for
// one selected deployment.
type deploymentsState struct {
	// appUUID is the application the list belongs to, used to detect when the
	// selection has moved and the list is stale.
	appUUID string
	list    []coolify.Deployment
	cursor  int
	loading bool
	err     error

	// openUUID is the deployment whose logs are showing; empty means the list.
	openUUID   string
	detail     coolify.Deployment
	detailErr  error
	detailBusy bool

	// follow keeps the log view pinned to the newest output.
	follow bool
	// polling guards against two poll loops running for one deployment.
	polling string
}

// showingLogs reports whether the log viewer is open.
func (d deploymentsState) showingLogs() bool { return d.openUUID != "" }

// selected returns the deployment under the cursor.
func (d deploymentsState) selected() (coolify.Deployment, bool) {
	if d.cursor < 0 || d.cursor >= len(d.list) {
		return coolify.Deployment{}, false
	}
	return d.list[d.cursor], true
}

// fetchDeployments loads an application's deployment history.
func (m Dashboard) fetchDeployments(appUUID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		list, err := client.ApplicationDeployments(ctx, appUUID, 0, deploymentHistoryLimit)
		return deploymentsMsg{appUUID: appUUID, list: list, err: err}
	}
}

// fetchDeploymentDetail loads one deployment, including its logs.
func (m Dashboard) fetchDeploymentDetail(uuid string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		dep, err := client.Deployment(ctx, uuid)
		return deploymentDetailMsg{uuid: uuid, dep: dep, err: err}
	}
}

// scheduleDeploymentPoll arms the log poll for a running build.
func scheduleDeploymentPoll(uuid string) tea.Cmd {
	return tea.Tick(deploymentPollInterval, func(time.Time) tea.Msg {
		return deploymentPollMsg{uuid: uuid}
	})
}

// ensureDeployments loads the history when the tab is opened, or when the
// selected application has changed since the list was fetched.
func (m *Dashboard) ensureDeployments() tea.Cmd {
	app, ok := m.tree.selectedApp()
	if !ok {
		return nil
	}
	if m.deployments.appUUID == app.UUID && (m.deployments.list != nil || m.deployments.err != nil) {
		return nil
	}
	if m.deployments.loading && m.deployments.appUUID == app.UUID {
		return nil
	}

	// Switching applications invalidates the whole tab, including any open log
	// viewer, which belongs to the previous application.
	m.deployments = deploymentsState{appUUID: app.UUID, loading: true}
	return m.fetchDeployments(app.UUID)
}

// reloadDeployments forces a history reload, for use after an action that added
// a deployment. ensureDeployments would consider the existing list current.
func (m *Dashboard) reloadDeployments(appUUID string) tea.Cmd {
	m.deployments = deploymentsState{appUUID: appUUID, loading: true}
	m.syncMain()
	return tea.Batch(m.fetchDeployments(appUUID), m.spin.Tick)
}

// handleDeploymentsMsg folds a fetched history into the model.
func (m Dashboard) handleDeploymentsMsg(msg deploymentsMsg) (tea.Model, tea.Cmd) {
	// Ignore a response for an application the user has already navigated away
	// from, otherwise a slow request would overwrite the current list.
	if msg.appUUID != m.deployments.appUUID {
		return m, nil
	}

	m.deployments.loading = false
	m.deployments.err = msg.err
	if msg.err == nil {
		m.deployments.list = msg.list
		if m.deployments.cursor >= len(msg.list) {
			m.deployments.cursor = max(0, len(msg.list)-1)
		}
	}
	m.syncMain()

	// Opening straight into a running build is what the user almost always
	// wants after triggering a deploy.
	if msg.err == nil && !m.deployments.showingLogs() {
		for i, dep := range msg.list {
			if dep.InProgress() {
				m.deployments.cursor = i
				return m.openDeploymentLogs()
			}
		}
	}
	return m, nil
}

// openDeploymentLogs switches to the log viewer for the selected deployment.
func (m Dashboard) openDeploymentLogs() (tea.Model, tea.Cmd) {
	dep, ok := m.deployments.selected()
	if !ok {
		return m, nil
	}
	m.deployments.openUUID = dep.DeploymentUUID
	m.deployments.detail = dep
	m.deployments.detailErr = nil
	m.deployments.detailBusy = true
	// Follow by default for a running build, since new output is the point.
	m.deployments.follow = dep.InProgress()
	m.focus = paneMain
	m.syncMain()
	m.main.GotoTop()

	return m, tea.Batch(m.fetchDeploymentDetail(dep.DeploymentUUID), m.spin.Tick)
}

// closeDeploymentLogs returns to the history list.
func (m Dashboard) closeDeploymentLogs() (tea.Model, tea.Cmd) {
	m.deployments.openUUID = ""
	m.deployments.detail = coolify.Deployment{}
	m.deployments.detailErr = nil
	m.deployments.detailBusy = false
	m.deployments.follow = false
	m.deployments.polling = ""
	m.syncMain()
	m.main.GotoTop()
	return m, nil
}

// handleDeploymentDetailMsg folds fetched logs into the model.
func (m Dashboard) handleDeploymentDetailMsg(msg deploymentDetailMsg) (tea.Model, tea.Cmd) {
	// A response for a deployment that is no longer open is stale.
	if msg.uuid != m.deployments.openUUID {
		return m, nil
	}

	m.deployments.detailBusy = false
	if msg.err != nil {
		m.deployments.detailErr = msg.err
		m.syncMain()
		return m, nil
	}

	previous := m.deployments.detail
	m.deployments.detail = msg.dep
	m.deployments.detailErr = nil

	// Keep the history row's status in step, so the list does not contradict the
	// log view the user is looking at.
	for i := range m.deployments.list {
		if m.deployments.list[i].DeploymentUUID == msg.uuid {
			m.deployments.list[i].Status = msg.dep.Status
			m.deployments.list[i].UpdatedAt = msg.dep.UpdatedAt
		}
	}
	m.syncMain()

	var cmds []tea.Cmd

	// Keep polling while the build runs.
	if msg.dep.InProgress() {
		if m.deployments.polling != msg.uuid {
			m.deployments.polling = msg.uuid
		}
		cmds = append(cmds, scheduleDeploymentPoll(msg.uuid), m.spin.Tick)
	} else {
		m.deployments.polling = ""
		// The build just finished: refresh the inventory so the application's
		// status catches up without waiting for the next tick.
		if previous.InProgress() && !m.loading {
			m.loading = true
			cmds = append(cmds,
				m.notify(fmt.Sprintf("build %s: %s", msg.dep.ShortCommit(), msg.dep.Status),
					isFailedStatus(msg.dep.Status)),
				m.fetchInventory(), m.spin.Tick)
		}
	}

	return m, tea.Batch(cmds...)
}

// handleDeploymentPoll re-fetches a running build's logs.
func (m Dashboard) handleDeploymentPoll(msg deploymentPollMsg) (tea.Model, tea.Cmd) {
	if msg.uuid != m.deployments.openUUID || m.deployments.detailBusy {
		return m, nil
	}
	m.deployments.detailBusy = true
	return m, m.fetchDeploymentDetail(msg.uuid)
}

// handleDeploymentsKey routes keys for the Deployments tab.
func (m Dashboard) handleDeploymentsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	keys := m.keys

	if m.deployments.showingLogs() {
		switch {
		case matches(msg, keys.Escape):
			model, cmd := m.closeDeploymentLogs()
			return model, cmd, true
		case matches(msg, keys.Follow):
			m.deployments.follow = !m.deployments.follow
			// The header carries the follow hint, so it has to be re-rendered.
			m.syncMain()
			if m.deployments.follow {
				m.main.GotoBottom()
			}
			return m, nil, true
		case matches(msg, keys.Bottom):
			m.deployments.follow = true
			m.syncMain()
			m.main.GotoBottom()
			return m, nil, true
		case matches(msg, keys.Top):
			// Scrolling away from the tail is an explicit request to stop being
			// yanked back to the bottom.
			m.deployments.follow = false
			m.syncMain()
			m.main.GotoTop()
			return m, nil, true
		case matches(msg, keys.Up), matches(msg, keys.PageUp):
			if m.deployments.follow {
				m.deployments.follow = false
				m.syncMain()
			}
		}
		return m, nil, false
	}

	switch {
	case matches(msg, keys.Up):
		m.deployments.cursor = max(0, m.deployments.cursor-1)
		m.syncMain()
		return m, nil, true
	case matches(msg, keys.Down):
		m.deployments.cursor = min(len(m.deployments.list)-1, m.deployments.cursor+1)
		if m.deployments.cursor < 0 {
			m.deployments.cursor = 0
		}
		m.syncMain()
		return m, nil, true
	case matches(msg, keys.Top):
		m.deployments.cursor = 0
		m.syncMain()
		return m, nil, true
	case matches(msg, keys.Bottom):
		m.deployments.cursor = max(0, len(m.deployments.list)-1)
		m.syncMain()
		return m, nil, true
	case matches(msg, keys.Select):
		model, cmd := m.openDeploymentLogs()
		return model, cmd, true
	}
	return m, nil, false
}

// renderDeploymentsList renders the history for the selected application.
func (m Dashboard) renderDeploymentsList(app coolify.Application, width int) string {
	s := m.styles
	state := m.deployments

	var lines []string
	lines = append(lines, "  "+s.Bold.Render("Deployments")+s.Faint.Render("  "+app.Name))

	switch {
	case state.loading && state.list == nil:
		lines = append(lines, "", "  "+m.spin.View()+s.Muted.Render(" Loading history…"))
		return strings.Join(lines, "\n")

	case state.err != nil:
		lines = append(lines, "",
			"  "+s.Danger.Render("Could not load the deployment history"),
			"",
			indent(wrap(state.err.Error(), width-4), "  "))
		if coolify.IsUnauthorized(state.err) {
			lines = append(lines, "",
				"  "+s.Faint.Render("The API token needs the read permission for deployments."))
		}
		return strings.Join(lines, "\n")

	case len(state.list) == 0:
		lines = append(lines, "",
			"  "+s.Faint.Render("No deployments recorded for this application yet."),
			"",
			"  "+s.Faint.Render("Press d to deploy it."))
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "  "+s.Faint.Render("enter opens the build log"), "")

	for i, dep := range state.list {
		lines = append(lines, m.renderDeploymentRow(dep, i == state.cursor, width))
	}
	return strings.Join(lines, "\n")
}

// renderDeploymentRow renders one history entry.
func (m Dashboard) renderDeploymentRow(dep coolify.Deployment, isCursor bool, width int) string {
	s := m.styles

	// The cursor row is styled as a whole, so its parts stay unstyled to avoid
	// an interior reset cutting the highlight.
	glyph := plainDeploymentGlyph(dep.Status)
	status := displayDeploymentStatus(dep.Status)
	commit := dep.ShortCommit()
	if commit == "" {
		commit = "—"
	}

	when := formatRelative(dep.CreatedAt)
	took := formatDuration(dep.Duration())

	trigger := "manual"
	switch {
	case dep.IsWebhook:
		trigger = "webhook"
	case dep.IsAPI:
		trigger = "api"
	}
	if dep.Rollback {
		trigger = "rollback"
	}
	if dep.ForceRebuild {
		trigger += " no-cache"
	}

	label := fmt.Sprintf("%-11s %-8s %-7s %-11s %s",
		truncatePlain(status, 11), commit, took, truncatePlain(when, 11), trigger)

	prefix := "  " + glyph + " "
	if !isCursor {
		prefix = "  " + s.DeploymentGlyph(dep.Status) + " "
	}

	line := composeRow(prefix, label, "", width, s, isCursor)
	if isCursor {
		if m.focus == paneMain {
			return s.Selected.Render(line)
		}
		return s.Badge.Render(line)
	}
	return line
}

// renderDeploymentLogs renders the build log for the open deployment.
func (m Dashboard) renderDeploymentLogs(width int) string {
	s := m.styles
	state := m.deployments
	dep := state.detail

	header := []string{
		"  " + s.DeploymentIndicator(dep.Status) +
			s.Faint.Render("  "+dep.ShortCommit()+"  "+formatDuration(dep.Duration())),
	}
	if dep.CommitMessage != "" {
		header = append(header, "  "+s.Faint.Render(truncatePlain(dep.CommitMessage, max(10, width-4))))
	}

	hint := "esc back to history · f follow"
	if state.follow {
		hint = "esc back to history · f unfollow · following"
	}
	header = append(header, "  "+s.Faint.Render(hint), "")

	if state.detailErr != nil {
		header = append(header,
			"  "+s.Danger.Render("Could not load the build log"),
			"",
			indent(wrap(state.detailErr.Error(), width-4), "  "))
		if coolify.IsUnauthorized(state.detailErr) {
			header = append(header, "",
				"  "+s.Faint.Render("Build logs need the read:sensitive token permission."))
		}
		return strings.Join(header, "\n")
	}

	lines := coolify.ParseDeploymentLogs(dep.Logs)
	if len(lines) == 0 {
		note := "No output yet."
		if dep.InProgress() {
			note = m.spin.View() + s.Muted.Render(" Waiting for build output…")
			header = append(header, "  "+note)
			return strings.Join(header, "\n")
		}
		header = append(header, "  "+s.Faint.Render(note))
		return strings.Join(header, "\n")
	}

	body := make([]string, 0, len(lines))
	for _, line := range lines {
		body = append(body, m.renderLogLine(line, width))
	}
	return strings.Join(append(header, body...), "\n")
}

// renderLogLine renders one build log entry, wrapping long output.
func (m Dashboard) renderLogLine(line coolify.LogLine, width int) string {
	s := m.styles
	avail := max(20, width-4)

	// A command entry is the build step being run; showing it as a shell prompt
	// makes the log skimmable.
	if line.Command != "" {
		return indent(s.Accent.Render(hardWrap("$ "+line.Command, avail)), "  ")
	}

	text := strings.TrimRight(line.Output, "\r\n")
	if text == "" {
		return ""
	}
	style := s.Value
	if line.Stderr() {
		style = s.Danger
	}
	// hardWrap preserves build output's own alignment, which docker and npm rely
	// on for their progress and table output.
	return indent(style.Render(hardWrap(text, avail)), "  ")
}

// displayDeploymentStatus shortens Coolify's longest status names so the history
// column does not have to truncate them.
func displayDeploymentStatus(status string) string {
	switch strings.ToLower(status) {
	case "cancelled_by_user":
		return "cancelled"
	case "in_progress":
		return "building"
	}
	return status
}

// isFailedStatus reports whether a deployment status means failure.
func isFailedStatus(status string) bool {
	switch strings.ToLower(status) {
	case "failed", "error", "cancelled_by_user", "cancelled":
		return true
	}
	return false
}

// plainDeploymentGlyph is the unstyled deployment glyph, matching
// Styles.DeploymentGlyph.
func plainDeploymentGlyph(status string) string {
	switch strings.ToLower(status) {
	case "finished", "success":
		return "✓"
	case "failed", "error":
		return "✕"
	case "in_progress", "running":
		return "▶"
	case "queued":
		return "…"
	case "cancelled_by_user", "cancelled":
		return "⊘"
	default:
		return "•"
	}
}
