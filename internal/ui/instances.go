package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/itsnitinr/coolify-tui/internal/config"
	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// instancePicker is the overlay for switching between configured Coolify
// instances.
type instancePicker struct {
	open   bool
	cursor int
}

// openInstancePicker shows the switcher, with the cursor on the current
// instance.
func (m Dashboard) openInstancePicker() (tea.Model, tea.Cmd) {
	if len(m.cfg.Instances) < 2 {
		return m, m.notify(
			"Only one instance is configured. Add another with `coolify-tui login`.", false)
	}
	m.picker.open = true
	m.picker.cursor = 0
	for i, inst := range m.cfg.Instances {
		if strings.EqualFold(inst.Name, m.instance.Name) {
			m.picker.cursor = i
		}
	}
	return m, nil
}

// handleInstancePickerKey routes keys while the switcher is open.
func (m Dashboard) handleInstancePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case matches(msg, m.keys.Up):
		m.picker.cursor = max(0, m.picker.cursor-1)
		return m, nil
	case matches(msg, m.keys.Down):
		m.picker.cursor = min(len(m.cfg.Instances)-1, m.picker.cursor+1)
		return m, nil
	case matches(msg, m.keys.Top):
		m.picker.cursor = 0
		return m, nil
	case matches(msg, m.keys.Bottom):
		m.picker.cursor = len(m.cfg.Instances) - 1
		return m, nil
	case matches(msg, m.keys.Select):
		return m.switchInstance()
	case matches(msg, m.keys.Escape), matches(msg, m.keys.Instances), matches(msg, m.keys.Quit):
		m.picker.open = false
		return m, nil
	}
	return m, nil
}

// switchInstance points the dashboard at the selected instance.
func (m Dashboard) switchInstance() (tea.Model, tea.Cmd) {
	if m.picker.cursor < 0 || m.picker.cursor >= len(m.cfg.Instances) {
		m.picker.open = false
		return m, nil
	}
	target := m.cfg.Instances[m.picker.cursor]
	m.picker.open = false

	if strings.EqualFold(target.Name, m.instance.Name) {
		return m, nil
	}

	client, err := newInstanceClient(target)
	if err != nil {
		// A token_env that is not exported is the usual cause, and the message
		// from ResolveToken names the variable.
		return m, m.notify("cannot switch to "+target.Name+": "+firstLine(err.Error()), true)
	}

	m.instance = target
	m.client = client

	// Everything on screen belonged to the previous instance. Nothing carries
	// over: not the inventory, not the tabs, not an in-flight action's identity.
	m.inv = coolify.Inventory{}
	m.version = ""
	m.loadErr = nil
	m.lastRefresh = m.lastRefresh.Truncate(0)
	m.deployments = deploymentsState{}
	m.logs = logsState{timestamps: m.logs.timestamps}
	m.envs = envsState{}
	m.inFlight = map[string]actionKind{}
	m.pending = nil
	m.tab = tabDetails
	m.focus = paneSidebar
	m.tree = newTreeModel()
	m.tree.setSize(m.tree.width, m.tree.height)
	m.loading = true
	m.layout()
	m.syncMain()

	// Remember the choice, but a config write failure must not block the switch.
	var cmds []tea.Cmd
	if err := m.cfg.SetActive(target.Name); err == nil {
		if err := m.cfg.Save(); err != nil {
			cmds = append(cmds, m.notify("switched, but could not save the active instance: "+
				firstLine(err.Error()), true))
		}
	}

	cmds = append(cmds,
		m.notify("switched to "+target.Name, false),
		m.fetchInventory(), m.fetchVersion(), m.spin.Tick)
	return m, tea.Batch(cmds...)
}

// newInstanceClient builds an API client for a configured instance.
func newInstanceClient(inst config.Instance) (*coolify.Client, error) {
	token, err := inst.ResolveToken()
	if err != nil {
		return nil, err
	}
	return coolify.New(inst.URL, token,
		coolify.WithInsecureSkipVerify(inst.InsecureSkipVerify),
		coolify.WithUserAgent("coolify-tui"),
	)
}

// viewInstancePicker renders the switcher overlay.
func (m Dashboard) viewInstancePicker() string {
	s := m.styles

	width := clamp(m.width*3/5, 40, 76)
	rowWidth := width

	lines := []string{
		s.Title.Render("Switch instance"),
		"",
	}

	for i, inst := range m.cfg.Instances {
		current := strings.EqualFold(inst.Name, m.instance.Name)
		isCursor := i == m.picker.cursor

		marker := "  "
		if current {
			marker = "● "
		}

		// Where the token comes from is the thing most worth showing here: a
		// token_env that is not exported is why a switch fails.
		source := "config file"
		if inst.TokenEnv != "" {
			source = "$" + inst.TokenEnv
		}

		label := fmt.Sprintf("%-16s %s", truncatePlain(inst.Name, 16), truncatePlain(inst.URL, 40))
		line := composeRow(marker, label, source, rowWidth, s, isCursor)
		if isCursor {
			lines = append(lines, s.Selected.Render(line))
			continue
		}
		lines = append(lines, line)
	}

	lines = append(lines,
		"",
		s.HelpBar.Render("↑/↓ choose · enter switch · esc cancel"),
		s.Faint.Render(wrap("Switching discards the current view and reloads from the "+
			"chosen instance. The choice is saved as the default.", width)),
	)

	card := s.Modal.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
}
