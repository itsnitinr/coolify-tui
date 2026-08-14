package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/itsnitinr/coolify-tui/internal/config"
	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// pane identifies which side of the layout has keyboard focus.
type pane int

const (
	paneSidebar pane = iota
	paneMain
)

// tab identifies the active detail tab.
type tab int

const (
	tabDetails tab = iota
	tabLogs
	tabDeployments
	tabEnv
	tabCount
)

func (t tab) title() string {
	switch t {
	case tabDetails:
		return "Details"
	case tabLogs:
		return "Logs"
	case tabDeployments:
		return "Deployments"
	case tabEnv:
		return "Env"
	}
	return ""
}

// Layout constants. The sidebar is proportional but clamped, so it stays usable
// on both an 80-column terminal and an ultrawide one.
const (
	sidebarMinWidth = 26
	sidebarMaxWidth = 46
	minUsableWidth  = 60
	minUsableHeight = 14
)

// --- Messages ---

// inventoryMsg carries the result of a background inventory fetch.
type inventoryMsg struct {
	inv coolify.Inventory
	err error
}

// tickMsg fires on the refresh interval.
type tickMsg time.Time

// toastExpiredMsg retires the toast with the given id.
type toastExpiredMsg struct{ id int }

// toast is a transient notification in the bottom-right corner.
type toast struct {
	id      int
	message string
	isError bool
}

// Dashboard is the root model: sidebar tree on the left, tabbed detail pane on
// the right, header and help bar top and bottom.
type Dashboard struct {
	cfg      *config.Config
	instance config.Instance
	client   *coolify.Client
	styles   Styles
	keys     KeyMap
	help     help.Model
	spin     spinner.Model

	inv         coolify.Inventory
	version     string
	loadErr     error
	loading     bool
	lastRefresh time.Time

	tree  treeModel
	focus pane
	tab   tab
	main  viewport.Model

	filtering   bool
	filterInput textinput.Model

	showHelp  bool
	toasts    []toast
	nextToast int

	width  int
	height int
	ready  bool
}

// NewDashboard builds the dashboard model for one instance.
func NewDashboard(cfg *config.Config, instance config.Instance, client *coolify.Client, styles Styles) Dashboard {
	helpModel := help.New()
	helpModel.Styles.ShortKey = styles.Accent
	helpModel.Styles.ShortDesc = styles.Faint
	helpModel.Styles.ShortSeparator = styles.Faint
	helpModel.Styles.FullKey = styles.Accent
	helpModel.Styles.FullDesc = styles.Faint
	helpModel.Styles.FullSeparator = styles.Faint

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.Spinner

	filter := textinput.New()
	filter.Prompt = ""
	filter.Placeholder = "filter applications"
	filter.CharLimit = 60
	filter.TextStyle = styles.Value
	filter.PlaceholderStyle = styles.Faint

	return Dashboard{
		cfg:         cfg,
		instance:    instance,
		client:      client,
		styles:      styles,
		keys:        DefaultKeyMap(),
		help:        helpModel,
		spin:        sp,
		tree:        newTreeModel(),
		filterInput: filter,
		loading:     true,
	}
}

// Init implements tea.Model.
func (m Dashboard) Init() tea.Cmd {
	return tea.Batch(
		m.fetchInventory(),
		m.fetchVersion(),
		m.spin.Tick,
		m.scheduleTick(),
	)
}

// --- Commands ---

// fetchInventory loads the full inventory in the background.
func (m Dashboard) fetchInventory() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		inv, err := client.FetchInventory(ctx)
		return inventoryMsg{inv: inv, err: err}
	}
}

// fetchVersion loads the instance version once, for the header.
func (m Dashboard) fetchVersion() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		version, err := client.Version(ctx)
		if err != nil {
			return versionMsg{}
		}
		return versionMsg{version: version}
	}
}

type versionMsg struct{ version string }

// scheduleTick arms the refresh timer.
func (m Dashboard) scheduleTick() tea.Cmd {
	interval := m.cfg.RefreshInterval.Std()
	if interval <= 0 {
		interval = config.DefaultRefreshInterval
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// notify queues a toast and schedules its expiry.
func (m *Dashboard) notify(message string, isError bool) tea.Cmd {
	m.nextToast++
	id := m.nextToast
	m.toasts = append(m.toasts, toast{id: id, message: message, isError: isError})

	linger := 4 * time.Second
	if isError {
		// Errors stay longer: they carry information the user needs to read.
		linger = 8 * time.Second
	}
	return tea.Tick(linger, func(time.Time) tea.Msg { return toastExpiredMsg{id: id} })
}

// --- Update ---

// Update implements tea.Model.
func (m Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.layout()
		m.syncMain()
		return m, nil

	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case versionMsg:
		m.version = msg.version
		return m, nil

	case tickMsg:
		// Skip a beat rather than stacking requests when one is still in
		// flight; Coolify rate-limits at 200 requests/minute.
		if m.loading {
			return m, m.scheduleTick()
		}
		m.loading = true
		return m, tea.Batch(m.fetchInventory(), m.spin.Tick, m.scheduleTick())

	case inventoryMsg:
		return m.handleInventory(msg)

	case toastExpiredMsg:
		m.toasts = removeToast(m.toasts, msg.id)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleInventory folds a completed fetch into the model.
func (m Dashboard) handleInventory(msg inventoryMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.loadErr = msg.err
		// Keep showing the last good inventory: a transient failure should not
		// blank a dashboard that is being watched.
		if len(m.inv.Servers) == 0 && len(m.inv.Apps) == 0 {
			m.syncMain()
			return m, nil
		}
		return m, m.notify("refresh failed: "+firstLine(msg.err.Error()), true)
	}

	m.loadErr = nil
	m.inv = msg.inv
	m.lastRefresh = msg.inv.FetchedAt
	m.tree.rebuild(m.inv)
	m.layout()
	m.syncMain()
	return m, nil
}

// handleKey routes a keystroke, honouring modal states first.
func (m Dashboard) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The filter input owns most keys while it is open.
	if m.filtering {
		return m.handleFilterKey(msg)
	}

	// ctrl+c always quits, even from the help overlay.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	if m.showHelp {
		switch {
		case key.Matches(msg, m.keys.Help, m.keys.Escape, m.keys.Quit):
			m.showHelp = false
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Help):
		m.showHelp = true
		return m, nil

	case key.Matches(msg, m.keys.Refresh):
		if m.loading {
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.fetchInventory(), m.spin.Tick)

	case key.Matches(msg, m.keys.Filter):
		m.filtering = true
		m.filterInput.SetValue(m.tree.filter)
		return m, m.filterInput.Focus()

	case key.Matches(msg, m.keys.NextPane), key.Matches(msg, m.keys.PrevPane):
		// Only two panes, so both directions toggle.
		if m.focus == paneSidebar {
			m.focus = paneMain
		} else {
			m.focus = paneSidebar
		}
		return m, nil

	case key.Matches(msg, m.keys.FocusSidebar):
		m.focus = paneSidebar
		return m, nil

	case key.Matches(msg, m.keys.FocusMain):
		m.focus = paneMain
		return m, nil

	case key.Matches(msg, m.keys.NextTab):
		m.tab = (m.tab + 1) % tabCount
		m.syncMain()
		return m, nil

	case key.Matches(msg, m.keys.PrevTab):
		m.tab = (m.tab - 1 + tabCount) % tabCount
		m.syncMain()
		return m, nil

	case key.Matches(msg, m.keys.TabDetails):
		m.tab = tabDetails
		m.syncMain()
		return m, nil

	case key.Matches(msg, m.keys.TabLogs):
		m.tab = tabLogs
		m.syncMain()
		return m, nil

	case key.Matches(msg, m.keys.TabDeployments):
		m.tab = tabDeployments
		m.syncMain()
		return m, nil

	case key.Matches(msg, m.keys.TabEnv):
		m.tab = tabEnv
		m.syncMain()
		return m, nil

	case key.Matches(msg, m.keys.OpenBrowser):
		return m.openInBrowser()
	}

	if m.focus == paneSidebar {
		return m.handleSidebarKey(msg)
	}
	return m.handleMainKey(msg)
}

// handleSidebarKey moves the tree cursor.
func (m Dashboard) handleSidebarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	before := m.tree.cursor
	switch {
	case key.Matches(msg, m.keys.Up):
		m.tree.moveUp(1)
	case key.Matches(msg, m.keys.Down):
		m.tree.moveDown(1)
	case key.Matches(msg, m.keys.PageUp):
		m.tree.moveUp(max(1, m.tree.height-1))
	case key.Matches(msg, m.keys.PageDown):
		m.tree.moveDown(max(1, m.tree.height-1))
	case key.Matches(msg, m.keys.Top):
		m.tree.gotoTop()
	case key.Matches(msg, m.keys.Bottom):
		m.tree.gotoBottom()
	case key.Matches(msg, m.keys.Collapse):
		m.tree.toggleCollapse(m.inv)
		m.syncMain()
		return m, nil
	case key.Matches(msg, m.keys.Select):
		row, ok := m.tree.selected()
		if !ok {
			return m, nil
		}
		if row.kind == rowServer {
			m.tree.toggleCollapse(m.inv)
			m.syncMain()
			return m, nil
		}
		// Selecting an application hands focus to its detail pane.
		m.focus = paneMain
		return m, nil
	default:
		return m, nil
	}

	if m.tree.cursor != before {
		m.syncMain()
	}
	return m, nil
}

// handleMainKey scrolls the detail pane.
func (m Dashboard) handleMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Escape):
		m.focus = paneSidebar
		return m, nil
	case key.Matches(msg, m.keys.Top):
		m.main.GotoTop()
		return m, nil
	case key.Matches(msg, m.keys.Bottom):
		m.main.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	m.main, cmd = m.main.Update(msg)
	return m, cmd
}

// handleFilterKey drives the filter prompt.
func (m Dashboard) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Abandon the filter entirely.
		m.filtering = false
		m.filterInput.Blur()
		m.filterInput.SetValue("")
		m.tree.setFilter("", m.inv)
		m.layout()
		m.syncMain()
		return m, nil
	case "enter":
		// Keep the filter but return focus to the tree.
		m.filtering = false
		m.filterInput.Blur()
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	m.tree.setFilter(m.filterInput.Value(), m.inv)
	m.layout()
	m.syncMain()
	return m, cmd
}

// openInBrowser opens the selected application's first domain, or the instance
// dashboard when no application is selected.
func (m Dashboard) openInBrowser() (tea.Model, tea.Cmd) {
	target := m.client.DashboardURL()
	label := "instance dashboard"
	if app, ok := m.tree.selectedApp(); ok {
		if domain := app.PrimaryDomain(); domain != "" {
			target, label = domain, domain
		}
	}
	if err := openURL(target); err != nil {
		return m, m.notify("could not open browser: "+err.Error(), true)
	}
	return m, m.notify("opened "+label, false)
}

// --- Layout ---

// layout recomputes pane dimensions from the terminal size.
func (m *Dashboard) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	sidebarWidth := m.width * 30 / 100
	sidebarWidth = clamp(sidebarWidth, sidebarMinWidth, sidebarMaxWidth)
	// Never let the sidebar crowd out the detail pane on a narrow terminal.
	if sidebarWidth > m.width/2 {
		sidebarWidth = m.width / 2
	}

	headerHeight := 2 // header line + blank separator
	footerHeight := 1 // help bar
	if m.filtering || m.tree.filter != "" {
		footerHeight++
	}
	bodyHeight := m.height - headerHeight - footerHeight
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	// Both panels render as: 1 border row, 1 header row (panel title or tab
	// strip), N content rows, 1 border row. So the scrollable region is three
	// rows shorter than the space the panel occupies.
	contentHeight := bodyHeight - 3
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Borders also consume one cell on each side horizontally.
	m.tree.setSize(sidebarWidth-2, contentHeight)

	mainWidth := m.width - sidebarWidth - 2
	if mainWidth < 10 {
		mainWidth = 10
	}
	m.main.Width = mainWidth
	m.main.Height = contentHeight
	m.help.Width = m.width
}

// syncMain re-renders the active tab into the scrollable detail pane, keeping
// the scroll position when the content is unchanged.
func (m *Dashboard) syncMain() {
	width := m.main.Width
	if width <= 0 {
		width = 60
	}

	var content string
	row, hasRow := m.tree.selected()

	switch {
	case !hasRow:
		content = m.renderEmptyState(width)
	case m.tab == tabDetails && row.kind == rowServer:
		content = m.renderServerDetails(row.server, width)
	case m.tab == tabDetails:
		content = m.renderAppDetails(row.app, width)
	case row.kind == rowServer:
		content = m.renderPlaceholder(
			m.tab.title(),
			"Select an application to see its "+strings.ToLower(m.tab.title())+".")
	case m.tab == tabLogs:
		content = m.renderPlaceholder("Container logs",
			"Live log streaming arrives in phase 6.")
	case m.tab == tabDeployments:
		content = m.renderPlaceholder("Deployments",
			"Deployment history and build logs arrive in phase 5.")
	case m.tab == tabEnv:
		content = m.renderPlaceholder("Environment variables",
			"The masked env-var viewer arrives in phase 6.")
	}

	atBottom := m.main.AtBottom()
	m.main.SetContent(content)
	if atBottom && m.tab == tabLogs {
		m.main.GotoBottom()
	}
}

// renderEmptyState explains an empty sidebar.
func (m Dashboard) renderEmptyState(width int) string {
	s := m.styles
	if m.loading && len(m.inv.Apps) == 0 {
		return "\n  " + m.spin.View() + s.Muted.Render(" Loading inventory…")
	}
	if m.loadErr != nil {
		return strings.Join([]string{
			"",
			"  " + s.Danger.Render("Could not load the inventory"),
			"",
			indent(wrap(m.loadErr.Error(), width-4), "  "),
			"",
			"  " + s.Faint.Render("ctrl+r retries · q quits"),
		}, "\n")
	}
	if m.tree.filter != "" {
		return "\n  " + s.Faint.Render(fmt.Sprintf("Nothing matches %q. Press / to change the filter.", m.tree.filter))
	}
	return strings.Join([]string{
		"",
		"  " + s.Bold.Render("No applications yet"),
		"",
		"  " + s.Faint.Render("This Coolify instance has no applications, or the token's team"),
		"  " + s.Faint.Render("cannot see them. Tokens are scoped to a single team."),
	}, "\n")
}

// --- View ---

// View implements tea.Model.
func (m Dashboard) View() string {
	if !m.ready {
		return "\n  " + m.spin.View() + m.styles.Muted.Render(" Starting…")
	}
	if m.width < minUsableWidth || m.height < minUsableHeight {
		return m.styles.Warning.Render(fmt.Sprintf(
			"Terminal too small: %d×%d. Need at least %d×%d.",
			m.width, m.height, minUsableWidth, minUsableHeight))
	}
	if m.showHelp {
		return m.viewHelpOverlay()
	}

	sidebar := m.viewSidebar()
	main := m.viewMain()
	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, main)

	sections := []string{m.viewHeader(), body}
	if footer := m.viewFilterBar(); footer != "" {
		sections = append(sections, footer)
	}
	sections = append(sections, m.viewHelpBar())

	screen := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return m.overlayToasts(screen)
}

// viewHeader renders the top line: instance identity on the left, live state on
// the right.
func (m Dashboard) viewHeader() string {
	s := m.styles

	left := s.Title.Render("coolify") + s.Faint.Render(" ▸ ") + s.Bold.Render(m.instance.Name)
	if m.version != "" {
		left += s.Faint.Render("  v" + m.version)
	}

	running, degraded, stopped := m.inv.Counts()
	segments := []string{
		s.Success.Render(fmt.Sprintf("● %d", running)),
	}
	if degraded > 0 {
		segments = append(segments, s.Warning.Render(fmt.Sprintf("◍ %d", degraded)))
	}
	if stopped > 0 {
		segments = append(segments, s.Danger.Render(fmt.Sprintf("○ %d", stopped)))
	}
	segments = append(segments, s.Faint.Render(fmt.Sprintf("%d servers", len(m.inv.Servers))))

	if deploying := len(m.inv.RunningDeployments()); deploying > 0 {
		segments = append(segments, s.Info.Render(fmt.Sprintf("▶ %d deploying", deploying)))
	}

	switch {
	case m.loading:
		segments = append(segments, m.spin.View()+s.Faint.Render(" refreshing"))
	case m.loadErr != nil:
		segments = append(segments, s.Danger.Render("⚠ stale"))
	case !m.lastRefresh.IsZero():
		segments = append(segments, s.Faint.Render(formatRelative(m.lastRefresh)))
	}
	if len(m.inv.Warnings) > 0 {
		segments = append(segments, s.Warning.Render(fmt.Sprintf("⚠ %d", len(m.inv.Warnings))))
	}

	right := strings.Join(segments, s.Faint.Render(" · "))

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Not enough room for both: the instance identity matters more.
		return clampWidth(left, m.width) + "\n"
	}
	return left + strings.Repeat(" ", gap) + right + "\n"
}

// viewSidebar renders the bordered tree panel.
func (m Dashboard) viewSidebar() string {
	s := m.styles
	focused := m.focus == paneSidebar

	panel := s.Panel
	if focused {
		panel = s.PanelFocused
	}

	title := "APPLICATIONS"
	if m.tree.filter != "" {
		title = fmt.Sprintf("APPLICATIONS · %d match", len(m.tree.rows))
	}

	// PanelTitle pads by one cell on each side, so the text has two fewer cells
	// than the panel's content width.
	inner := lipgloss.JoinVertical(lipgloss.Left,
		s.PanelTitle.Render(truncatePlain(title, max(1, m.tree.width-2))),
		m.tree.view(s, focused),
	)
	return panel.Width(m.tree.width).Height(m.tree.height + 1).Render(inner)
}

// viewMain renders the bordered detail panel with its tab strip.
func (m Dashboard) viewMain() string {
	s := m.styles
	focused := m.focus == paneMain

	panel := s.Panel
	if focused {
		panel = s.PanelFocused
	}

	inner := lipgloss.JoinVertical(lipgloss.Left,
		m.viewTabStrip(),
		m.main.View(),
	)
	return panel.Width(m.main.Width).Height(m.main.Height + 1).Render(inner)
}

// viewTabStrip renders the tab headers with the active one highlighted.
//
// The strip must never wrap: an extra line would push the panel past its
// allotted height and tear the layout. So it degrades to numbers only, and is
// hard-clamped to the panel width as a last resort.
func (m Dashboard) viewTabStrip() string {
	s := m.styles

	build := func(withTitles bool) string {
		parts := make([]string, 0, int(tabCount))
		for t := tabDetails; t < tabCount; t++ {
			label := fmt.Sprintf(" %d ", int(t)+1)
			if withTitles {
				label = fmt.Sprintf(" %d %s ", int(t)+1, t.title())
			}
			if t == m.tab {
				parts = append(parts, s.Badge.Render(label))
				continue
			}
			parts = append(parts, s.Faint.Render(label))
		}
		return strings.Join(parts, s.Faint.Render("│"))
	}

	strip := build(true)
	if lipgloss.Width(strip) > m.main.Width {
		strip = build(false)
		// With numbers alone the active tab needs naming somewhere.
		if title := " " + m.tab.title(); lipgloss.Width(strip)+lipgloss.Width(title) <= m.main.Width {
			strip += s.Muted.Render(title)
		}
	}

	// Show the scroll position when the pane overflows.
	if m.main.Height > 0 && !(m.main.AtTop() && m.main.AtBottom()) {
		percent := fmt.Sprintf(" %3.0f%%", m.main.ScrollPercent()*100)
		gap := m.main.Width - lipgloss.Width(strip) - lipgloss.Width(percent)
		if gap > 0 {
			strip += strings.Repeat(" ", gap) + s.Faint.Render(percent)
		}
	}
	return clampWidth(strip, m.main.Width)
}

// viewFilterBar renders the filter prompt or the active filter summary.
func (m Dashboard) viewFilterBar() string {
	s := m.styles
	switch {
	case m.filtering:
		return clampWidth(s.Accent.Render(" / ")+m.filterInput.View(), m.width)
	case m.tree.filter != "":
		return clampWidth(
			s.Faint.Render(fmt.Sprintf(" filter: %q — / to edit, esc to clear", m.tree.filter)),
			m.width)
	}
	return ""
}

// viewHelpBar renders the one-line key hints.
func (m Dashboard) viewHelpBar() string {
	if m.loadErr != nil {
		return m.styles.Danger.Render(" ⚠ " +
			truncatePlain(firstLine(m.loadErr.Error()), max(10, m.width-4)))
	}
	// help.Model.Width is advisory: bubbles keeps the item that overflows when
	// there is no room for its ellipsis, so clamp the result ourselves.
	return clampWidth(m.help.ShortHelpView(m.keys.ShortHelp()), m.width)
}

// viewHelpOverlay renders the full keybinding reference.
func (m Dashboard) viewHelpOverlay() string {
	s := m.styles

	sections := []string{
		s.Title.Render("coolify-tui") + s.Faint.Render("  keybindings"),
		"",
		m.help.FullHelpView(m.keys.FullHelp()),
		"",
		s.Faint.Render("Status glyphs: ") +
			s.Success.Render("● running") + s.Faint.Render(" · ") +
			s.Warning.Render("◍ degraded (up, failing health check)") + s.Faint.Render(" · ") +
			s.Danger.Render("○ stopped") + s.Faint.Render(" · ") +
			s.Info.Render("▶ deploying"),
		"",
		s.Faint.Render("instance: ") + s.Value.Render(m.instance.Name+" — "+m.client.BaseURL()),
		s.Faint.Render("config:   ") + s.Value.Render(m.cfg.Path()),
	}

	// Warnings are counted in the header but have nowhere else to be read, so
	// the overlay is where their detail lives.
	if len(m.inv.Warnings) > 0 {
		sections = append(sections, "",
			s.Warning.Render(fmt.Sprintf("⚠ %d warning(s) from the last refresh",
				len(m.inv.Warnings))))
		for _, warning := range m.inv.Warnings {
			sections = append(sections, "  "+s.Faint.Render(truncatePlain(warning, max(20, m.width-12))))
		}
	}
	if warn := m.cfg.PermissionWarning(); warn != "" {
		sections = append(sections, "", s.Warning.Render("⚠ "+warn))
	}

	sections = append(sections, "", s.HelpBar.Render("? or esc closes this"))

	card := s.Modal.Render(lipgloss.JoinVertical(lipgloss.Left, sections...))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
}

// overlayToasts draws notifications over the bottom-right of the screen.
func (m Dashboard) overlayToasts(screen string) string {
	if len(m.toasts) == 0 {
		return screen
	}

	cards := make([]string, 0, len(m.toasts))
	maxWidth := m.width / 2
	if maxWidth < 24 {
		maxWidth = 24
	}
	for _, t := range m.toasts {
		style := m.styles.Toast
		if t.isError {
			style = m.styles.ToastError
		}
		cards = append(cards, style.Render(wrap(t.message, maxWidth)))
	}
	stack := lipgloss.JoinVertical(lipgloss.Right, cards...)

	// Anchor above the help bar, right-aligned.
	lines := strings.Split(screen, "\n")
	stackLines := strings.Split(stack, "\n")
	top := len(lines) - 1 - len(stackLines)
	if top < 1 {
		top = 1
	}
	for i, overlayLine := range stackLines {
		row := top + i
		if row >= len(lines) {
			break
		}
		lines[row] = overlayRight(lines[row], overlayLine, m.width)
	}
	return strings.Join(lines, "\n")
}

// overlayRight draws overlay over the right-hand end of base, keeping the line
// exactly width cells.
//
// Styled text cannot be sliced safely — a cut can land inside an escape
// sequence — so base is never truncated. When base's own content would reach
// under the overlay, base is dropped for that row and the overlay is padded to
// stay right-aligned. Toasts are transient, so briefly covering a row reads
// better than a toast that drifts to the left margin.
func overlayRight(base, overlay string, width int) string {
	overlayW := lipgloss.Width(overlay)
	if overlayW >= width {
		return clampWidth(overlay, width)
	}
	keep := width - overlayW

	// Trailing padding is the common case on a mostly-empty row, and is free to
	// discard.
	trimmed := strings.TrimRight(base, " ")
	if trimmedW := lipgloss.Width(trimmed); trimmedW <= keep {
		return trimmed + strings.Repeat(" ", keep-trimmedW) + overlay
	}
	return strings.Repeat(" ", keep) + overlay
}

// --- Helpers ---

func removeToast(toasts []toast, id int) []toast {
	out := toasts[:0]
	for _, t := range toasts {
		if t.id != id {
			out = append(out, t)
		}
	}
	return out
}

// firstLine reduces a multi-line error to its first line, for one-line displays.
func firstLine(str string) string {
	if idx := strings.IndexByte(str, '\n'); idx >= 0 {
		return str[:idx]
	}
	return str
}

// clampWidth truncates a styled line to width display cells. lipgloss MaxWidth
// is ANSI-aware, so unlike a string slice it cannot cut an escape sequence.
func clampWidth(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

func clamp(value, lo, hi int) int {
	if value < lo {
		return lo
	}
	if value > hi {
		return hi
	}
	return value
}

// Run starts the dashboard and blocks until the user quits.
func Run(cfg *config.Config, instance config.Instance, client *coolify.Client, styles Styles) error {
	model := NewDashboard(cfg, instance, client, styles)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}

// errNoBrowser is returned when no opener is available on this platform.
var errNoBrowser = errors.New("no browser opener available")
