package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// testInventory builds a small inventory: two servers, one with three apps and
// one with one, plus an app that belongs to no known server.
//
// Servers and applications are listed in the order FetchInventory produces —
// sorted by server name, then application name — because the sidebar renders
// them in inventory order rather than re-sorting.
func testInventory() coolify.Inventory {
	return coolify.Inventory{
		Servers: []coolify.Server{
			{UUID: "srv-a", Name: "alpha", Settings: coolify.ServerSettings{IsReachable: true, IsUsable: true}},
			{UUID: "srv-b", Name: "beta", Settings: coolify.ServerSettings{IsReachable: true, IsUsable: true}},
		},
		Apps: []coolify.Application{
			{UUID: "a2", Name: "api", Status: "running:unhealthy", ServerUUID: "srv-a", ServerName: "alpha", GitBranch: "main"},
			{UUID: "a1", Name: "web", Status: "running:healthy", ServerUUID: "srv-a", ServerName: "alpha", GitBranch: "main"},
			{UUID: "a3", Name: "worker", Status: "exited:unhealthy", ServerUUID: "srv-a", ServerName: "alpha"},
			{UUID: "a4", Name: "docs", Status: "running:healthy", ServerUUID: "srv-b", ServerName: "beta"},
			{UUID: "a5", Name: "orphan", Status: "running:healthy", ServerName: "unknown"},
		},
	}
}

func TestTreeRebuildNestsAppsUnderServers(t *testing.T) {
	inv := testInventory()
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.rebuild(inv)

	var got []string
	for _, row := range tree.rows {
		if row.kind == rowServer {
			name := row.server.Name
			if name == "" {
				name = "unknown server"
			}
			got = append(got, "S:"+name)
			continue
		}
		got = append(got, "  A:"+row.app.Name)
	}

	want := []string{
		"S:alpha", "  A:api", "  A:web", "  A:worker",
		"S:beta", "  A:docs",
		"S:unknown server", "  A:orphan",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestTreeServerRowCountsHealth(t *testing.T) {
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.rebuild(testInventory())

	for _, row := range tree.rows {
		if row.kind != rowServer || row.server.Name != "alpha" {
			continue
		}
		if row.appCount != 3 {
			t.Errorf("alpha appCount = %d, want 3", row.appCount)
		}
		if row.degraded != 1 {
			t.Errorf("alpha degraded = %d, want 1 (api is running:unhealthy)", row.degraded)
		}
		if row.stopped != 1 {
			t.Errorf("alpha stopped = %d, want 1 (worker is exited)", row.stopped)
		}
		return
	}
	t.Fatal("no row for server alpha")
}

func TestTreeSyntheticRowIsNotAnUnreachableServer(t *testing.T) {
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.rebuild(testInventory())

	for _, row := range tree.rows {
		if row.kind == rowServer && row.server.UUID == "" {
			if !row.synthetic {
				t.Error("the orphan bucket should be marked synthetic")
			}
			return
		}
	}
	t.Fatal("no synthetic row for applications with no known server")
}

func TestTreeCursorSurvivesRebuild(t *testing.T) {
	inv := testInventory()
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.rebuild(inv)

	// Select "worker" on alpha.
	for i, row := range tree.rows {
		if row.kind == rowApp && row.app.Name == "worker" {
			tree.cursor = i
		}
	}

	// A refresh where an earlier application disappeared must keep the cursor on
	// the same application rather than on the same index.
	trimmed := inv
	trimmed.Apps = append([]coolify.Application{}, inv.Apps[1:]...)
	tree.rebuild(trimmed)

	row, ok := tree.selected()
	if !ok || row.kind != rowApp || row.app.Name != "worker" {
		t.Errorf("selected row = %+v, want the worker application", row)
	}
}

func TestTreeCursorClampsWhenSelectionDisappears(t *testing.T) {
	inv := testInventory()
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.rebuild(inv)
	tree.gotoBottom()

	tree.rebuild(coolify.Inventory{
		Servers: []coolify.Server{{UUID: "srv-a", Name: "alpha"}},
		Apps:    []coolify.Application{{UUID: "a1", Name: "web", ServerUUID: "srv-a"}},
	})
	if tree.cursor >= len(tree.rows) || tree.cursor < 0 {
		t.Errorf("cursor = %d, out of range for %d rows", tree.cursor, len(tree.rows))
	}
}

func TestTreeCollapseHidesChildren(t *testing.T) {
	inv := testInventory()
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.rebuild(inv)

	before := len(tree.rows)
	tree.cursor = 0 // the alpha server row
	tree.toggleCollapse(inv)

	if len(tree.rows) != before-3 {
		t.Errorf("rows after collapsing alpha = %d, want %d", len(tree.rows), before-3)
	}
	row, _ := tree.selected()
	if row.kind != rowServer || row.server.Name != "alpha" {
		t.Errorf("cursor after collapse = %+v, want to stay on alpha", row)
	}

	tree.toggleCollapse(inv)
	if len(tree.rows) != before {
		t.Errorf("rows after expanding = %d, want %d", len(tree.rows), before)
	}
}

func TestTreeCollapseFromChildFoldsParent(t *testing.T) {
	inv := testInventory()
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.rebuild(inv)

	// Put the cursor on an application, then fold.
	tree.cursor = 1
	tree.toggleCollapse(inv)

	row, ok := tree.selected()
	if !ok || row.kind != rowServer || row.server.UUID != "srv-a" {
		t.Errorf("selected = %+v, want the parent server row", row)
	}
	if !tree.collapsed["srv-a"] {
		t.Error("folding from a child should collapse its parent")
	}
}

func TestTreeFilterMatchesNameDomainAndBranch(t *testing.T) {
	inv := testInventory()
	inv.Apps[3].FQDN = "https://docs.example.com"

	tests := []struct {
		filter string
		want   []string
	}{
		{"web", []string{"web"}},
		{"WEB", []string{"web"}},
		{"docs.example", []string{"docs"}},
		{"main", []string{"api", "web"}},
		{"nothing-matches", nil},
	}
	for _, tc := range tests {
		tree := newTreeModel()
		tree.setSize(30, 20)
		tree.setFilter(tc.filter, inv)

		var got []string
		for _, row := range tree.rows {
			if row.kind == rowApp {
				got = append(got, row.app.Name)
			}
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("filter %q matched %v, want %v", tc.filter, got, tc.want)
		}
	}
}

func TestTreeFilterHidesServersWithNoMatches(t *testing.T) {
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.setFilter("docs", testInventory())

	for _, row := range tree.rows {
		if row.kind == rowServer && row.server.Name != "beta" {
			t.Errorf("server %q should be hidden when nothing on it matches", row.server.Name)
		}
	}
}

func TestTreeFilterOverridesCollapse(t *testing.T) {
	inv := testInventory()
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.rebuild(inv)
	tree.collapsed["srv-a"] = true
	tree.rebuild(inv)

	// Folded: alpha's applications are hidden.
	for _, row := range tree.rows {
		if row.kind == rowApp && row.app.ServerUUID == "srv-a" {
			t.Fatal("collapsed server should hide its applications")
		}
	}

	// Filtering should reveal matches regardless of the fold, otherwise a
	// search would silently miss results.
	tree.setFilter("web", inv)
	found := false
	for _, row := range tree.rows {
		if row.kind == rowApp && row.app.Name == "web" {
			found = true
		}
	}
	if !found {
		t.Error("a filter match inside a collapsed server should still be shown")
	}
}

func TestTreeNavigationClamps(t *testing.T) {
	inv := testInventory()
	tree := newTreeModel()
	tree.setSize(30, 4)
	tree.rebuild(inv)

	tree.moveUp(50)
	if tree.cursor != 0 {
		t.Errorf("cursor after moving up past the top = %d, want 0", tree.cursor)
	}
	tree.moveDown(500)
	if tree.cursor != len(tree.rows)-1 {
		t.Errorf("cursor after moving down past the bottom = %d, want %d", tree.cursor, len(tree.rows)-1)
	}
	// The window must have scrolled to keep the cursor visible.
	if tree.cursor < tree.offset || tree.cursor >= tree.offset+tree.height {
		t.Errorf("cursor %d not visible in window [%d,%d)", tree.cursor, tree.offset, tree.offset+tree.height)
	}

	tree.gotoTop()
	if tree.cursor != 0 || tree.offset != 0 {
		t.Errorf("gotoTop left cursor=%d offset=%d", tree.cursor, tree.offset)
	}
}

func TestTreeSelectedAppAndServer(t *testing.T) {
	inv := testInventory()
	tree := newTreeModel()
	tree.setSize(30, 20)
	tree.rebuild(inv)

	// Row 0 is a server row: it has no application.
	if _, ok := tree.selectedApp(); ok {
		t.Error("selectedApp() on a server row should report false")
	}
	srv, ok := tree.selectedServer(inv)
	if !ok || srv.UUID != "srv-a" {
		t.Errorf("selectedServer() = %+v, want srv-a", srv)
	}

	// Row 1 is an application: its parent server should resolve.
	tree.cursor = 1
	app, ok := tree.selectedApp()
	if !ok {
		t.Fatal("selectedApp() on an application row should report true")
	}
	if app.ServerUUID != "srv-a" {
		t.Errorf("app.ServerUUID = %q, want srv-a", app.ServerUUID)
	}
	srv, ok = tree.selectedServer(inv)
	if !ok || srv.UUID != "srv-a" {
		t.Errorf("selectedServer() from an app row = %+v, want srv-a", srv)
	}
}

func TestTreeViewRowsAreExactlyWidth(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	inv := testInventory()
	// A name long enough to force truncation.
	inv.Apps[0].Name = "a-very-long-application-name-that-will-not-fit-in-the-sidebar"

	for _, width := range []int{12, 20, 26, 34, 46} {
		tree := newTreeModel()
		tree.setSize(width, 20)
		tree.rebuild(inv)

		for i, line := range strings.Split(tree.view(styles, true, nil), "\n") {
			if got := lipgloss.Width(line); got != width {
				t.Errorf("width %d: row %d rendered %d cells:\n%q", width, i, got, line)
			}
		}
	}
}

func TestTreeSelectedRowHasNoInteriorReset(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	tree := newTreeModel()
	tree.setSize(34, 20)
	tree.rebuild(testInventory())
	tree.cursor = 1 // an application row, which carries a status glyph

	view := tree.view(styles, true, nil)
	selected := strings.Split(view, "\n")[1]

	// A nested colour span emits its own reset, which would end the selection
	// background partway along the row. The highlight must be one span.
	if resets := strings.Count(selected, "\x1b[0m"); resets > 1 {
		t.Errorf("selected row has %d resets, want at most 1 (highlight would break):\n%q",
			resets, selected)
	}
}

func TestTreeViewEmptyStates(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	tree := newTreeModel()
	tree.setSize(30, 10)
	tree.rebuild(coolify.Inventory{})
	if !strings.Contains(tree.view(styles, true, nil), "No applications") {
		t.Error("an empty inventory should say so")
	}

	tree.setFilter("zzz", testInventory())
	if !strings.Contains(tree.view(styles, true, nil), "zzz") {
		t.Error("an empty filter result should name the filter")
	}
}

func TestComposeRowExactWidth(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	tests := []struct {
		name   string
		prefix string
		label  string
		right  string
		width  int
	}{
		{"fits", "▾ ● ", "alpha", "3", 30},
		{"label truncated", "▾ ● ", strings.Repeat("long", 20), "3", 24},
		{"right dropped", "   ● ", "application", "very-long-branch-name", 20},
		{"styled prefix", "   " + styles.Success.Render("●") + " ", "web", "main", 28},
		{"styled right", "▾ ", "alpha", styles.Warning.Render("2◍") + " 5", 30},
		{"wide runes", "▾ ● ", "日本語アプリケーション名", "3", 22},
		{"tiny", "▾ ● ", "alpha", "3", 6},
	}
	for _, tc := range tests {
		for _, plain := range []bool{false, true} {
			got := composeRow(tc.prefix, tc.label, tc.right, tc.width, styles, plain)
			if w := lipgloss.Width(got); w != tc.width {
				t.Errorf("%s (plain=%v): width = %d, want %d: %q", tc.name, plain, w, tc.width, got)
			}
		}
	}
}

func TestComposeRowZeroWidth(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	if got := composeRow("▾ ", "alpha", "3", 0, styles, false); got != "" {
		t.Errorf("composeRow with width 0 = %q, want empty", got)
	}
}

func TestTruncatePlain(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"hello", 1, "…"},
		{"hello", 0, ""},
		{"日本語", 3, "日…"},
	}
	for _, tc := range tests {
		got := truncatePlain(tc.in, tc.width)
		if got != tc.want {
			t.Errorf("truncatePlain(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
		if w := lipgloss.Width(got); w > tc.width {
			t.Errorf("truncatePlain(%q, %d) = %q, which is %d cells wide", tc.in, tc.width, got, w)
		}
	}
}
