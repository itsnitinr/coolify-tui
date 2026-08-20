package ui

import "github.com/charmbracelet/bubbles/key"

// KeyMap is the dashboard's complete set of bindings. It doubles as the source
// for both the one-line help bar and the full help overlay, so a binding can
// never drift from its documentation.
type KeyMap struct {
	// Navigation
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Top      key.Binding
	Bottom   key.Binding

	// Focus and layout
	NextPane     key.Binding
	PrevPane     key.Binding
	FocusSidebar key.Binding
	FocusMain    key.Binding
	Select       key.Binding
	Collapse     key.Binding

	// Tabs
	NextTab        key.Binding
	PrevTab        key.Binding
	TabDetails     key.Binding
	TabLogs        key.Binding
	TabDeployments key.Binding
	TabEnv         key.Binding

	// Actions
	Deploy      key.Binding
	ForceDeploy key.Binding
	Start       key.Binding
	Stop        key.Binding
	Restart     key.Binding
	Cancel      key.Binding
	Refresh     key.Binding
	Follow      key.Binding
	Timestamps  key.Binding
	Debug       key.Binding
	Reveal      key.Binding
	RevealAll   key.Binding
	OpenBrowser key.Binding

	// Global
	Filter    key.Binding
	Instances key.Binding
	Help      key.Binding
	Quit      key.Binding
	Escape    key.Binding

	// Confirmation modal
	ConfirmYes key.Binding
	ConfirmNo  key.Binding
}

// DefaultKeyMap returns the standard bindings. They follow lazydocker and
// lazygit conventions where those exist: hjkl movement, tab to cycle panes,
// bracket keys for tabs, ? for help.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "ctrl+b"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "ctrl+f"),
			key.WithHelp("pgdn", "page down"),
		),
		Top: key.NewBinding(
			key.WithKeys("g", "home"),
			key.WithHelp("g", "top"),
		),
		Bottom: key.NewBinding(
			key.WithKeys("G", "end"),
			key.WithHelp("G", "bottom"),
		),

		NextPane: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next pane"),
		),
		PrevPane: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("⇧tab", "prev pane"),
		),
		FocusSidebar: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "sidebar"),
		),
		FocusMain: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "detail"),
		),
		Select: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Collapse: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("space", "fold server"),
		),

		NextTab: key.NewBinding(
			key.WithKeys("]", "L"),
			key.WithHelp("]", "next tab"),
		),
		PrevTab: key.NewBinding(
			key.WithKeys("[", "H"),
			key.WithHelp("[", "prev tab"),
		),
		TabDetails: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "details"),
		),
		TabLogs: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "logs"),
		),
		TabDeployments: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "deployments"),
		),
		TabEnv: key.NewBinding(
			key.WithKeys("4"),
			key.WithHelp("4", "env"),
		),

		Deploy: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "deploy"),
		),
		ForceDeploy: key.NewBinding(
			key.WithKeys("D"),
			key.WithHelp("D", "deploy (no cache)"),
		),
		Start: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "start"),
		),
		Stop: key.NewBinding(
			key.WithKeys("x"),
			key.WithHelp("x", "stop"),
		),
		Restart: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "restart"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "cancel build"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("ctrl+r"),
			key.WithHelp("ctrl+r", "refresh"),
		),
		Follow: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "follow"),
		),
		Timestamps: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "timestamps"),
		),
		// "." follows the file-manager convention for toggling hidden entries,
		// and the letter keys a build log would want are already actions.
		Debug: key.NewBinding(
			key.WithKeys("."),
			key.WithHelp(".", "debug lines"),
		),
		Reveal: key.NewBinding(
			key.WithKeys("v"),
			key.WithHelp("v", "reveal value"),
		),
		RevealAll: key.NewBinding(
			key.WithKeys("V"),
			key.WithHelp("V", "reveal all values"),
		),
		OpenBrowser: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "open in browser"),
		),

		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
		Instances: key.NewBinding(
			key.WithKeys("i"),
			key.WithHelp("i", "switch instance"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Escape: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),

		ConfirmYes: key.NewBinding(
			key.WithKeys("y", "enter"),
			key.WithHelp("y", "confirm"),
		),
		ConfirmNo: key.NewBinding(
			key.WithKeys("n", "esc"),
			key.WithHelp("n", "cancel"),
		),
	}
}

// ShortHelp is the one-line hint strip along the bottom of the dashboard.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.Up, k.Down, k.NextPane, k.Select,
		k.Deploy, k.Restart, k.Filter, k.Help, k.Quit,
	}
}

// FullHelp is the grouped listing shown in the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom},
		{k.NextPane, k.PrevPane, k.FocusSidebar, k.FocusMain, k.Select, k.Collapse},
		{k.TabDetails, k.TabLogs, k.TabDeployments, k.TabEnv, k.PrevTab, k.NextTab},
		{k.Deploy, k.ForceDeploy, k.Start, k.Stop, k.Restart, k.Cancel},
		{k.Follow, k.Timestamps, k.Debug, k.Reveal, k.RevealAll, k.OpenBrowser, k.Refresh},
		{k.Filter, k.Instances, k.Help, k.Escape, k.Quit},
	}
}
