package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// actionKind is a mutating operation the dashboard can perform.
type actionKind int

const (
	actionDeploy actionKind = iota
	actionForceDeploy
	actionStart
	actionStop
	actionRestart
	actionCancelDeploy
)

// verb is the imperative name of the action, for prompts and toasts.
func (a actionKind) verb() string {
	switch a {
	case actionDeploy:
		return "Deploy"
	case actionForceDeploy:
		return "Deploy without cache"
	case actionStart:
		return "Start"
	case actionStop:
		return "Stop"
	case actionRestart:
		return "Restart"
	case actionCancelDeploy:
		return "Cancel deployment"
	}
	return "Act on"
}

// gerund describes the action in progress, for the in-flight indicator.
func (a actionKind) gerund() string {
	switch a {
	case actionDeploy, actionForceDeploy:
		return "deploying"
	case actionStart:
		return "starting"
	case actionStop:
		return "stopping"
	case actionRestart:
		return "restarting"
	case actionCancelDeploy:
		return "cancelling"
	}
	return "working"
}

// consequence explains what the action will actually do, so the confirmation
// prompt is informative rather than a reflex.
func (a actionKind) consequence() string {
	switch a {
	case actionDeploy:
		return "Queues a build and deployment from the current branch. " +
			"Docker layer cache is reused."
	case actionForceDeploy:
		return "Queues a build with the Docker layer cache disabled. " +
			"Slower, but picks up changes a cached build would miss."
	case actionStart:
		return "Starts the application's containers. Coolify implements this " +
			"as a deployment, so a build may run."
	case actionStop:
		return "Stops the application's containers. It will be unreachable " +
			"until started again."
	case actionRestart:
		return "Restarts the application's containers. Expect a brief outage."
	case actionCancelDeploy:
		return "Cancels the running build. The previously deployed version " +
			"keeps serving traffic."
	}
	return ""
}

// destructive reports whether the action interrupts traffic. Only these carry a
// red prompt.
func (a actionKind) destructive() bool {
	switch a {
	case actionStop, actionRestart, actionCancelDeploy:
		return true
	}
	return false
}

// permission names the Coolify token permission the action needs, for use in
// error messages when the API answers 403.
func (a actionKind) permission() string {
	switch a {
	case actionDeploy, actionForceDeploy:
		return "deploy"
	case actionCancelDeploy:
		return "write"
	default:
		return "write"
	}
}

// pendingAction is an action awaiting confirmation.
type pendingAction struct {
	kind actionKind
	app  coolify.Application
	// deploymentUUID is set for actionCancelDeploy.
	deploymentUUID string
}

// actionResultMsg reports the outcome of a mutating call.
type actionResultMsg struct {
	kind    actionKind
	appUUID string
	appName string
	result  coolify.ActionResult
	err     error
}

// refreshSoonMsg asks for an out-of-band refresh shortly after an action, since
// Coolify processes lifecycle changes on a queue and the status will not have
// settled by the time the call returns.
type refreshSoonMsg struct{}

// requestAction begins an action, prompting for confirmation when configured.
func (m Dashboard) requestAction(kind actionKind) (tea.Model, tea.Cmd) {
	app, ok := m.tree.selectedApp()
	if !ok {
		return m, m.notify("Select an application first — actions do not apply to a whole server.", true)
	}

	pending := pendingAction{kind: kind, app: app}

	if kind == actionCancelDeploy {
		dep, running := m.inv.DeploymentForApp(app.Name)
		if !running {
			return m, m.notify(app.Name+" has no deployment running.", true)
		}
		pending.deploymentUUID = dep.DeploymentUUID
	}

	// Refuse actions that cannot succeed, rather than asking the API to say no.
	if reason, blocked := m.actionBlocked(kind, app); blocked {
		return m, m.notify(reason, true)
	}

	if !m.cfg.ShouldConfirmDestructive() {
		return m.runAction(pending)
	}
	m.pending = &pending
	return m, nil
}

// actionBlocked reports why an action cannot apply to an application.
func (m Dashboard) actionBlocked(kind actionKind, app coolify.Application) (string, bool) {
	if existing, busy := m.inFlight[app.UUID]; busy {
		return fmt.Sprintf("%s is already %s.", app.Name, existing.gerund()), true
	}

	status := coolify.ParseStatus(app.Status)
	switch kind {
	case actionStart:
		if status.Running() {
			return app.Name + " is already running. Use r to restart it.", true
		}
	case actionStop:
		if !status.Running() {
			return app.Name + " is not running.", true
		}
	case actionRestart:
		if !status.Running() {
			return app.Name + " is not running. Use s to start it.", true
		}
	}
	return "", false
}

// confirmPending runs the action awaiting confirmation.
func (m Dashboard) confirmPending() (tea.Model, tea.Cmd) {
	if m.pending == nil {
		return m, nil
	}
	pending := *m.pending
	m.pending = nil
	return m.runAction(pending)
}

// runAction dispatches the API call and marks the application busy.
func (m Dashboard) runAction(pending pendingAction) (tea.Model, tea.Cmd) {
	if m.inFlight == nil {
		m.inFlight = map[string]actionKind{}
	}
	m.inFlight[pending.app.UUID] = pending.kind
	// Re-render so the pane reflects the action immediately. Without this the UI
	// would look unresponsive until the next refresh lands.
	m.syncMain()

	client := m.client
	app := pending.app
	kind := pending.kind
	deploymentUUID := pending.deploymentUUID

	call := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var (
			result coolify.ActionResult
			err    error
		)
		switch kind {
		case actionDeploy, actionForceDeploy:
			var results []coolify.ActionResult
			results, err = client.Deploy(ctx, []string{app.UUID}, kind == actionForceDeploy)
			if len(results) > 0 {
				result = results[0]
			}
		case actionStart:
			result, err = client.StartApplication(ctx, app.UUID, false, false)
		case actionStop:
			result, err = client.StopApplication(ctx, app.UUID)
		case actionRestart:
			result, err = client.RestartApplication(ctx, app.UUID)
		case actionCancelDeploy:
			result, err = client.CancelDeployment(ctx, deploymentUUID)
		}

		return actionResultMsg{
			kind:    kind,
			appUUID: app.UUID,
			appName: app.Name,
			result:  result,
			err:     err,
		}
	}

	return m, tea.Batch(call, m.spin.Tick)
}

// handleActionResult folds an action's outcome into the model.
func (m Dashboard) handleActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
	delete(m.inFlight, msg.appUUID)
	m.syncMain()

	if msg.err != nil {
		return m, m.notify(describeActionError(msg), true)
	}

	message := strings.TrimSpace(msg.result.Message)
	if message == "" {
		message = msg.kind.verb() + " request accepted"
	}
	summary := fmt.Sprintf("%s: %s", msg.appName, message)

	// Refresh straight away for the optimistic case, then again shortly after,
	// because Coolify queues the work and the first refresh usually still shows
	// the old status.
	cmds := []tea.Cmd{
		m.notify(summary, false),
		tea.Tick(2*time.Second, func(time.Time) tea.Msg { return refreshSoonMsg{} }),
	}
	if !m.loading {
		m.loading = true
		cmds = append(cmds, m.fetchInventory(), m.spin.Tick)
	}
	return m, tea.Batch(cmds...)
}

// describeActionError turns an API failure into advice.
func describeActionError(msg actionResultMsg) string {
	base := fmt.Sprintf("%s %s failed: %s",
		strings.ToLower(msg.kind.verb()), msg.appName, firstLine(msg.err.Error()))

	if coolify.IsUnauthorized(msg.err) {
		return base + fmt.Sprintf(" — the API token needs the %q permission.", msg.kind.permission())
	}
	if coolify.StatusCode(msg.err) == 429 {
		return base + " — Coolify is rate limiting; try again in a moment."
	}
	return base
}

// anyInFlight reports whether any action is running, so the spinner keeps
// ticking.
func (m Dashboard) anyInFlight() bool { return len(m.inFlight) > 0 }

// viewConfirmModal renders the confirmation prompt.
func (m Dashboard) viewConfirmModal() string {
	s := m.styles
	pending := m.pending

	accent := s.Theme.Accent
	glyph := s.Accent.Render("▶")
	if pending.kind.destructive() {
		accent = s.Theme.Danger
		glyph = s.Danger.Render("⚠")
	}

	width := clamp(m.width*3/5, 40, 72)

	target := s.Bold.Render(pending.app.Name)
	if pending.app.ServerName != "" {
		target += s.Faint.Render("  on " + pending.app.ServerName)
	}

	lines := []string{
		glyph + " " + lipgloss.NewStyle().Foreground(accent).Bold(true).Render(pending.kind.verb()),
		"",
		"  " + target,
	}
	if domain := pending.app.PrimaryDomain(); domain != "" {
		lines = append(lines, "  "+s.Faint.Render(domain))
	}
	lines = append(lines,
		"  "+s.Faint.Render("currently ")+s.StatusIndicator(pending.app.Status),
		"",
		s.Muted.Render(wrap(pending.kind.consequence(), width)),
		"",
		s.Accent.Render("y")+s.Faint.Render("/")+s.Accent.Render("enter")+
			s.Faint.Render(" confirm    ")+
			s.Accent.Render("n")+s.Faint.Render("/")+s.Accent.Render("esc")+
			s.Faint.Render(" cancel"),
	)

	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(1, 3).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
}
