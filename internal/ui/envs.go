package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// envsMsg carries fetched environment variables.
type envsMsg struct {
	appUUID string
	vars    []coolify.EnvVar
	err     error
}

// envsState is the Env tab: a read-only variable list, masked by default.
type envsState struct {
	appUUID string
	vars    []coolify.EnvVar
	loading bool
	err     error
	cursor  int

	// revealed holds the UUIDs (or keys, when a variable has no UUID) of
	// variables whose values are shown.
	revealed map[string]bool
	// revealAll shows every value at once.
	revealAll bool
}

// envKey identifies a variable for reveal tracking. Coolify does not always
// populate uuid on env vars, so fall back to the key name.
func envKey(v coolify.EnvVar) string {
	if v.UUID != "" {
		return v.UUID
	}
	return v.Key
}

// isRevealed reports whether a variable's value should be shown.
func (e envsState) isRevealed(v coolify.EnvVar) bool {
	return e.revealAll || e.revealed[envKey(v)]
}

// selected returns the variable under the cursor.
func (e envsState) selected() (coolify.EnvVar, bool) {
	if e.cursor < 0 || e.cursor >= len(e.vars) {
		return coolify.EnvVar{}, false
	}
	return e.vars[e.cursor], true
}

// fetchEnvs loads an application's environment variables.
func (m Dashboard) fetchEnvs(appUUID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vars, err := client.ApplicationEnvs(ctx, appUUID)
		return envsMsg{appUUID: appUUID, vars: vars, err: err}
	}
}

// ensureEnvs loads variables when the tab is opened or the selection moves.
func (m *Dashboard) ensureEnvs() tea.Cmd {
	app, ok := m.tree.selectedApp()
	if !ok {
		return nil
	}
	if m.envs.appUUID == app.UUID && (m.envs.vars != nil || m.envs.err != nil || m.envs.loading) {
		return nil
	}

	// Reveals are deliberately not carried across applications: a value shown
	// for one application must not be shown for the next by accident.
	m.envs = envsState{appUUID: app.UUID, loading: true, revealed: map[string]bool{}}
	return tea.Batch(m.fetchEnvs(app.UUID), m.spin.Tick)
}

// handleEnvsMsg folds fetched variables into the model.
func (m Dashboard) handleEnvsMsg(msg envsMsg) (tea.Model, tea.Cmd) {
	if msg.appUUID != m.envs.appUUID {
		return m, nil
	}
	m.envs.loading = false
	m.envs.err = msg.err
	if msg.err == nil {
		m.envs.vars = msg.vars
		if m.envs.cursor >= len(msg.vars) {
			m.envs.cursor = max(0, len(msg.vars)-1)
		}
	}
	m.syncMain()
	return m, nil
}

// handleEnvsKey routes keys for the Env tab.
func (m Dashboard) handleEnvsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	keys := m.keys
	switch {
	case matches(msg, keys.Up):
		m.envs.cursor = max(0, m.envs.cursor-1)
		m.syncMain()
		return m, nil, true

	case matches(msg, keys.Down):
		m.envs.cursor = min(max(0, len(m.envs.vars)-1), m.envs.cursor+1)
		m.syncMain()
		return m, nil, true

	case matches(msg, keys.Top):
		m.envs.cursor = 0
		m.syncMain()
		m.main.GotoTop()
		return m, nil, true

	case matches(msg, keys.Bottom):
		m.envs.cursor = max(0, len(m.envs.vars)-1)
		m.syncMain()
		return m, nil, true

	case matches(msg, keys.Reveal):
		v, ok := m.envs.selected()
		if !ok {
			return m, nil, true
		}
		if m.envs.revealed == nil {
			m.envs.revealed = map[string]bool{}
		}
		key := envKey(v)
		m.envs.revealed[key] = !m.envs.revealed[key]
		m.syncMain()
		return m, nil, true

	case matches(msg, keys.RevealAll):
		m.envs.revealAll = !m.envs.revealAll
		m.syncMain()
		return m, nil, true
	}
	return m, nil, false
}

// renderEnvs renders the environment variable list.
func (m Dashboard) renderEnvs(app coolify.Application, width int) string {
	s := m.styles
	state := m.envs

	header := []string{
		"  " + s.Bold.Render("Environment variables") + s.Faint.Render("  "+app.Name),
	}

	switch {
	case state.loading && state.vars == nil:
		header = append(header, "", "  "+m.spin.View()+s.Muted.Render(" Loading variables…"))
		return strings.Join(header, "\n")

	case state.err != nil:
		header = append(header, "",
			"  "+s.Danger.Render("Could not read the environment variables"),
			"",
			indent(wrap(state.err.Error(), width-4), "  "))
		if coolify.IsUnauthorized(state.err) {
			header = append(header, "",
				indent(s.Faint.Render(wrap("Environment variables need the read:sensitive token "+
					"permission. A token with read alone can monitor and deploy, but cannot see "+
					"values.", width-4)), "  "))
		}
		return strings.Join(header, "\n")

	case len(state.vars) == 0:
		header = append(header, "", "  "+s.Faint.Render("No environment variables set."))
		return strings.Join(header, "\n")
	}

	hint := "v reveal the selected value · V reveal all"
	if state.revealAll {
		hint = "v mask one · V mask all"
	}
	header = append(header,
		"  "+s.Faint.Render(hint),
		"  "+s.Faint.Render("read-only — edit variables in the Coolify dashboard"))
	if state.revealAll {
		header = append(header,
			"  "+s.Warning.Render("⚠ every value is visible — careful if you are sharing this screen"))
	}
	header = append(header, "")

	// Align values on the longest key, within reason.
	keyWidth := 0
	for _, v := range state.vars {
		keyWidth = max(keyWidth, len(v.Key))
	}
	keyWidth = clamp(keyWidth, 8, 34)

	rows := make([]string, 0, len(state.vars))
	for i, v := range state.vars {
		rows = append(rows, m.renderEnvRow(v, keyWidth, i == state.cursor, width))
	}
	return strings.Join(append(header, rows...), "\n")
}

// renderEnvRow renders one variable.
func (m Dashboard) renderEnvRow(v coolify.EnvVar, keyWidth int, isCursor bool, width int) string {
	s := m.styles

	value := maskEnvValue(v.Resolved())
	if m.envs.isRevealed(v) {
		value = v.Resolved()
		if value == "" {
			value = "(empty)"
		}
	}

	// Scope flags explain why a variable might not be visible at runtime.
	var flags []string
	if v.IsBuildTime && !v.IsRuntime {
		flags = append(flags, "build-only")
	}
	if v.IsRuntime && !v.IsBuildTime {
		flags = append(flags, "runtime")
	}
	if v.IsShared {
		flags = append(flags, "shared")
	}
	if v.IsPreview {
		flags = append(flags, "preview")
	}
	if v.IsMultiline {
		flags = append(flags, "multiline")
	}
	// Flags go in the right-aligned segment, which composeRow never truncates.
	// Scope matters more than the tail of a long value, and inside the label a
	// long value would push it off the row entirely.
	right := strings.Join(flags, ",")

	// Multiline values are collapsed onto one row: a variable holding a private
	// key would otherwise take over the pane, and an embedded newline would break
	// the row's width arithmetic.
	value = strings.NewReplacer("\r\n", "⏎", "\n", "⏎", "\r", "⏎").Replace(value)

	label := fmt.Sprintf("%-*s  %s", keyWidth, truncatePlain(v.Key, keyWidth), value)

	marker := "  "
	if isCursor {
		marker = "▸ "
	}
	line := composeRow(marker, label, right, width, s, isCursor)
	if isCursor {
		if m.focus == paneMain {
			return s.Selected.Render(line)
		}
		return s.Badge.Render(line)
	}
	return line
}

// maskEnvValue renders a value as a mask that reveals nothing but its length.
// The length is useful — an empty value or a truncated paste is a common
// misconfiguration — and is not itself a secret.
func maskEnvValue(value string) string {
	if value == "" {
		return "(empty)"
	}
	runes := len([]rune(value))
	dots := clamp(runes, 3, 12)
	return strings.Repeat("•", dots) + fmt.Sprintf(" (%d chars)", runes)
}
