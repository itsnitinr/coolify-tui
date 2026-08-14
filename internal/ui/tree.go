package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// rowKind distinguishes the two kinds of sidebar row.
type rowKind int

const (
	rowServer rowKind = iota
	rowApp
)

// treeRow is one visible line in the sidebar.
type treeRow struct {
	kind   rowKind
	server coolify.Server
	app    coolify.Application

	// appCount and health summarise a server row's children.
	appCount int
	degraded int
	stopped  int

	// deploying marks an application with an in-flight deployment.
	deploying bool

	// synthetic marks the placeholder row that holds applications whose server
	// could not be determined. It is not a real server, so it must not be
	// rendered as an unreachable one.
	synthetic bool
}

// key uniquely identifies a row across rebuilds, so the cursor can be restored
// after a refresh reorders or replaces the underlying data.
func (r treeRow) key() string {
	if r.kind == rowServer {
		return "server:" + r.server.UUID
	}
	return "app:" + r.app.UUID
}

// treeModel is the sidebar: servers with their applications nested beneath.
type treeModel struct {
	rows   []treeRow
	cursor int
	offset int

	// collapsed tracks folded servers by UUID.
	collapsed map[string]bool
	// filter, when non-empty, restricts rows to matching applications.
	filter string

	width  int
	height int
}

func newTreeModel() treeModel {
	return treeModel{collapsed: map[string]bool{}}
}

// rebuild regenerates the rows from an inventory, preserving the selected row
// where possible.
func (t *treeModel) rebuild(inv coolify.Inventory) {
	var selectedKey string
	if row, ok := t.selected(); ok {
		selectedKey = row.key()
	}

	byServer := inv.AppsByServer()
	t.rows = t.rows[:0]

	appendServer := func(srv coolify.Server, apps []coolify.Application, synthetic bool) {
		matching := filterApps(apps, t.filter)
		// While filtering, hide servers with no matches so the list stays short.
		if t.filter != "" && len(matching) == 0 {
			return
		}

		srvRow := treeRow{kind: rowServer, server: srv, appCount: len(matching), synthetic: synthetic}
		for _, app := range matching {
			st := coolify.ParseStatus(app.Status)
			switch {
			case st.Degraded():
				srvRow.degraded++
			case !st.Running():
				srvRow.stopped++
			}
		}
		t.rows = append(t.rows, srvRow)

		// A filter implies the user wants to see the matches, so ignore folds.
		if t.collapsed[srv.UUID] && t.filter == "" {
			return
		}
		for _, app := range matching {
			_, deploying := inv.DeploymentForApp(app.Name)
			t.rows = append(t.rows, treeRow{kind: rowApp, app: app, deploying: deploying})
		}
	}

	for _, srv := range inv.Servers {
		appendServer(srv, byServer[srv.UUID], false)
	}

	// Applications whose server could not be determined still need a home,
	// otherwise a resource-listing failure would silently hide them.
	if orphans := byServer[""]; len(orphans) > 0 {
		appendServer(coolify.Server{Name: "unknown server", UUID: ""}, orphans, true)
	}

	t.restoreCursor(selectedKey)
}

// filterApps narrows applications by a case-insensitive substring match against
// the name, domain and git branch.
func filterApps(apps []coolify.Application, filter string) []coolify.Application {
	if filter == "" {
		return apps
	}
	needle := strings.ToLower(filter)
	out := make([]coolify.Application, 0, len(apps))
	for _, app := range apps {
		haystack := strings.ToLower(app.Name + " " + app.FQDN + " " + app.GitBranch + " " + app.Description)
		if strings.Contains(haystack, needle) {
			out = append(out, app)
		}
	}
	return out
}

// restoreCursor puts the cursor back on the row it was on before a rebuild,
// clamping when that row is gone.
func (t *treeModel) restoreCursor(key string) {
	if key != "" {
		for i, row := range t.rows {
			if row.key() == key {
				t.cursor = i
				t.clampOffset()
				return
			}
		}
	}
	t.clampCursor()
}

func (t *treeModel) clampCursor() {
	if t.cursor >= len(t.rows) {
		t.cursor = len(t.rows) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
	t.clampOffset()
}

// clampOffset scrolls the window so the cursor stays visible.
func (t *treeModel) clampOffset() {
	if t.height <= 0 {
		t.offset = 0
		return
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+t.height {
		t.offset = t.cursor - t.height + 1
	}
	maxOffset := len(t.rows) - t.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if t.offset > maxOffset {
		t.offset = maxOffset
	}
	if t.offset < 0 {
		t.offset = 0
	}
}

// selected returns the row under the cursor.
func (t treeModel) selected() (treeRow, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return treeRow{}, false
	}
	return t.rows[t.cursor], true
}

// selectedApp returns the selected application. When a server row is selected
// it reports false, since server rows have no application context.
func (t treeModel) selectedApp() (coolify.Application, bool) {
	row, ok := t.selected()
	if !ok || row.kind != rowApp {
		return coolify.Application{}, false
	}
	return row.app, true
}

// selectedServer returns the server context for the cursor: the server itself
// for a server row, or the parent server for an application row.
func (t treeModel) selectedServer(inv coolify.Inventory) (coolify.Server, bool) {
	row, ok := t.selected()
	if !ok {
		return coolify.Server{}, false
	}
	if row.kind == rowServer {
		return row.server, true
	}
	for _, srv := range inv.Servers {
		if srv.UUID == row.app.ServerUUID {
			return srv, true
		}
	}
	return coolify.Server{}, false
}

func (t *treeModel) moveUp(n int) {
	t.cursor -= n
	t.clampCursor()
}

func (t *treeModel) moveDown(n int) {
	t.cursor += n
	t.clampCursor()
}

func (t *treeModel) gotoTop() {
	t.cursor = 0
	t.clampOffset()
}

func (t *treeModel) gotoBottom() {
	t.cursor = len(t.rows) - 1
	t.clampCursor()
}

// toggleCollapse folds or unfolds the server under the cursor. When an
// application row is selected it folds that application's parent and moves the
// cursor to it, which is what "collapse where I am" should do.
func (t *treeModel) toggleCollapse(inv coolify.Inventory) {
	row, ok := t.selected()
	if !ok {
		return
	}
	uuid := row.server.UUID
	if row.kind == rowApp {
		uuid = row.app.ServerUUID
	}
	t.collapsed[uuid] = !t.collapsed[uuid]
	t.rebuild(inv)

	if t.collapsed[uuid] {
		for i, r := range t.rows {
			if r.kind == rowServer && r.server.UUID == uuid {
				t.cursor = i
				t.clampOffset()
				return
			}
		}
	}
}

// setSize records the sidebar's inner dimensions.
func (t *treeModel) setSize(width, height int) {
	t.width, t.height = width, height
	t.clampOffset()
}

// setFilter applies a filter and rebuilds.
func (t *treeModel) setFilter(filter string, inv coolify.Inventory) {
	t.filter = filter
	t.rebuild(inv)
}

// view renders the sidebar rows.
func (t treeModel) view(s Styles, focused bool) string {
	if len(t.rows) == 0 {
		msg := "No applications found."
		if t.filter != "" {
			msg = fmt.Sprintf("No matches for %q.", t.filter)
		}
		return s.Faint.Render(msg)
	}

	end := t.offset + t.height
	if end > len(t.rows) {
		end = len(t.rows)
	}
	start := t.offset
	if start > end {
		start = end
	}

	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		lines = append(lines, t.renderRow(s, t.rows[i], i == t.cursor, focused))
	}
	return strings.Join(lines, "\n")
}

// renderRow draws one row, truncating to the sidebar width.
func (t treeModel) renderRow(s Styles, row treeRow, isCursor, focused bool) string {
	// The cursor bar spans the full width, so the highlight reads as a
	// selection rather than as coloured text.
	width := t.width
	if width <= 0 {
		width = 30
	}

	// On the cursor row every glyph is rendered unstyled. A nested colour span
	// emits its own reset, which would cut the selection background partway
	// along the line; the glyph shapes still carry the status.
	plain := isCursor

	// prefix carries styling, so its display width is tracked separately;
	// label is plain text and is the only part ever truncated.
	var prefix, label, right string
	switch row.kind {
	case rowServer:
		fold := "▾"
		if t.collapsed[row.server.UUID] && t.filter == "" {
			fold = "▸"
		}
		label = row.server.Name
		if label == "" {
			label = "unknown server"
		}

		health := row.server.Health()
		dot := plainServerGlyph(health)
		if row.synthetic {
			// Not a real server: an empty Server would otherwise read as
			// unreachable, which would be a lie about a healthy instance.
			dot = "?"
		}
		if !plain {
			if row.synthetic {
				dot = s.Faint.Render(dot)
			} else {
				dot = s.ServerDot(health)
			}
		}
		prefix = fold + " " + dot + " "

		right = fmt.Sprintf("%d", row.appCount)
		if row.degraded > 0 {
			right = decorate(s.Warning, fmt.Sprintf("%d◍", row.degraded), plain) + " " + right
		}
		if row.stopped > 0 {
			right = decorate(s.Danger, fmt.Sprintf("%d○", row.stopped), plain) + " " + right
		}

	case rowApp:
		glyph := plainStatusGlyph(row.app.Status)
		if row.deploying {
			glyph = "▶"
		}
		if !plain {
			glyph = s.StatusDot(row.app.Status)
			if row.deploying {
				glyph = s.Info.Render("▶")
			}
		}
		prefix = "   " + glyph + " "
		label = row.app.Name
		right = row.app.GitBranch
	}

	line := composeRow(prefix, label, right, width, s, plain)
	if isCursor {
		if focused {
			return s.Selected.Render(line)
		}
		// Unfocused selection stays visible but recedes.
		return lipgloss.NewStyle().
			Background(s.Theme.SelectionBg).
			Foreground(s.Theme.Muted).
			Render(line)
	}
	return line
}

// decorate applies a style unless plain rendering was requested.
func decorate(style lipgloss.Style, text string, plain bool) string {
	if plain {
		return text
	}
	return style.Render(text)
}

// plainStatusGlyph is the unstyled status glyph, matching Styles.StatusDot.
func plainStatusGlyph(raw string) string {
	st := coolify.ParseStatus(raw)
	switch {
	case st.Degraded():
		return "◍"
	case st.Running():
		return "●"
	case st.State == "" || st.State == "unknown":
		return "○"
	case st.State == "restarting" || st.State == "starting":
		return "◐"
	default:
		return "○"
	}
}

// plainServerGlyph is the unstyled server glyph, matching Styles.ServerDot.
func plainServerGlyph(h coolify.ServerHealth) string {
	switch h {
	case coolify.ServerHealthy:
		return "●"
	case coolify.ServerUnreachable:
		return "✕"
	case coolify.ServerUnusable:
		return "◍"
	default:
		return "○"
	}
}

// composeRow lays out one row of exactly width display cells as
// prefix + label + padding + right.
//
// prefix and right may carry ANSI styling and are never cut, because slicing a
// styled string can land inside an escape sequence. Only label is plain text, so
// it is the only part truncated. When even the label cannot fit, right is
// dropped first.
//
// plain suppresses the dimming of the right segment, for rows whose styling is
// applied to the whole line afterwards.
func composeRow(prefix, label, right string, width int, s Styles, plain bool) string {
	if width <= 0 {
		return ""
	}
	prefixW := lipgloss.Width(prefix)
	rightW := lipgloss.Width(right)

	// Reserve room for the right segment plus at least one space of gap, but
	// only if that leaves a usable amount of space for the label.
	const minLabel = 6
	reserved := 0
	if rightW > 0 && width-prefixW-rightW-1 >= minLabel {
		reserved = rightW + 1
	}

	labelSpace := width - prefixW - reserved
	if labelSpace < 0 {
		labelSpace = 0
	}
	label = truncatePlain(label, labelSpace)
	labelW := lipgloss.Width(label)

	line := prefix + label
	gap := width - prefixW - labelW - rightW
	if reserved > 0 && gap > 0 {
		return line + strings.Repeat(" ", gap) + decorate(s.Faint, right, plain)
	}
	if pad := width - prefixW - labelW; pad > 0 {
		return line + strings.Repeat(" ", pad)
	}
	return line
}

// truncatePlain trims unstyled text to width display cells, adding an ellipsis
// when it does not fit. lipgloss.Width is cell-aware, so this stays correct for
// wide and combining characters.
func truncatePlain(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(text)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if candidate := string(runes) + "…"; lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return "…"
}
