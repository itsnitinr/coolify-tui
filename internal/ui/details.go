package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// detailWriter accumulates aligned label/value rows for a detail pane.
type detailWriter struct {
	styles Styles
	width  int
	lines  []string
}

func newDetailWriter(s Styles, width int) *detailWriter {
	return &detailWriter{styles: s, width: width}
}

// labelWidth is the column the values line up on.
const labelWidth = 15

// field writes one label/value row, wrapping long values under the label.
func (d *detailWriter) field(label, value string) {
	if value == "" {
		return
	}
	d.styledField(label, value, d.styles.Value)
}

// styledField writes a label/value row with an explicit value style.
func (d *detailWriter) styledField(label, value string, style lipgloss.Style) {
	if value == "" {
		return
	}
	labelCell := d.styles.Label.Render(fmt.Sprintf("%-*s", labelWidth, label))

	avail := d.width - labelWidth - 2
	if avail < 20 {
		avail = 20
	}
	wrapped := strings.Split(wrap(value, avail), "\n")
	for i, part := range wrapped {
		if i == 0 {
			d.lines = append(d.lines, "  "+labelCell+style.Render(part))
			continue
		}
		d.lines = append(d.lines, "  "+strings.Repeat(" ", labelWidth)+style.Render(part))
	}
}

// raw writes a pre-rendered line as-is.
func (d *detailWriter) raw(line string) { d.lines = append(d.lines, line) }

// blank writes an empty line.
func (d *detailWriter) blank() { d.lines = append(d.lines, "") }

// section writes a section heading.
func (d *detailWriter) section(title string) {
	d.blank()
	d.lines = append(d.lines, "  "+d.styles.Bold.Render(title))
}

func (d *detailWriter) String() string { return strings.Join(d.lines, "\n") }

// renderAppDetails renders the Details tab for an application.
func (m Dashboard) renderAppDetails(app coolify.Application, width int) string {
	s := m.styles
	d := newDetailWriter(s, width)

	d.raw("  " + s.Title.Render(app.Name))
	if app.Description != "" {
		d.raw("  " + s.Faint.Render(app.Description))
	}
	d.blank()
	d.raw("  " + s.StatusIndicator(app.Status))

	// An in-flight deployment is the most time-sensitive thing on this pane, so
	// it goes directly under the status.
	if dep, ok := m.inv.DeploymentForApp(app.Name); ok {
		d.blank()
		banner := fmt.Sprintf("▶ deploying %s · %s · %s",
			dep.ShortCommit(), dep.Status, formatDuration(dep.Duration()))
		d.raw("  " + s.Info.Render(banner))
		d.raw("  " + s.Faint.Render("press 3 for build logs, c to cancel"))
	}

	d.section("Placement")
	d.field("server", app.ServerName)
	if srv, ok := m.serverByUUID(app.ServerUUID); ok {
		d.styledField("server health", string(srv.Health()), m.serverHealthStyle(srv.Health()))
		if srv.IP != "" {
			d.field("server ip", srv.IP)
		}
	}

	if domains := app.Domains(); len(domains) > 0 {
		d.section("Domains")
		for _, domain := range domains {
			d.raw("  " + s.Link.Render(domain))
		}
		d.raw("  " + s.Faint.Render("press o to open the first domain in a browser"))
	}

	d.section("Source")
	switch {
	case app.GitRepository != "":
		d.field("repository", app.GitRepository)
		d.field("branch", app.GitBranch)
		if app.GitCommitSHA != "" {
			d.field("commit", app.ShortCommit())
		}
	case app.DockerRegistryImageName != "":
		image := app.DockerRegistryImageName
		if app.DockerRegistryImageTag != "" {
			image += ":" + app.DockerRegistryImageTag
		}
		d.field("image", image)
	}
	d.field("build pack", app.BuildPack)

	d.section("Runtime")
	d.field("ports exposed", app.PortsExposes)
	d.field("ports mapped", app.PortsMappings)
	if app.HealthCheckEnabled {
		path := app.HealthCheckPath
		if path == "" {
			path = "/"
		}
		d.styledField("health check", "enabled "+path, s.Success)
	} else {
		d.styledField("health check", "disabled", s.Faint)
	}
	d.field("memory limit", app.LimitsMemory)
	d.field("cpu limit", app.LimitsCPUs)

	d.section("Metadata")
	d.field("uuid", app.UUID)
	if !app.CreatedAt.IsZero() {
		d.field("created", app.CreatedAt.Local().Format("2006-01-02 15:04"))
	}
	if !app.UpdatedAt.IsZero() {
		d.field("updated", formatRelative(app.UpdatedAt))
	}

	return d.String()
}

// renderServerDetails renders the Details tab for a server.
func (m Dashboard) renderServerDetails(srv coolify.Server, width int) string {
	s := m.styles
	d := newDetailWriter(s, width)

	name := srv.Name
	if name == "" {
		name = "unknown server"
	}
	d.raw("  " + s.Title.Render(name))
	if srv.Description != "" {
		d.raw("  " + s.Faint.Render(srv.Description))
	}

	// The synthetic bucket is not a real server. Health() on a zero Server would
	// report "unreachable", so bail out before rendering any health at all.
	if srv.UUID == "" {
		d.blank()
		d.raw("  " + s.Warning.Render("These applications could not be matched to a server."))
		d.raw(indent(s.Faint.Render(wrap("Coolify reports an application's server via the "+
			"per-server resource listing. If that call failed, the applications still "+
			"appear here so they are not silently hidden. Press ? to read the refresh "+
			"warnings.", width-4)), "  "))
		return d.String()
	}

	d.blank()
	d.raw("  " + s.ServerIndicator(srv.Health()))

	d.section("Connection")
	if srv.IP != "" {
		address := srv.IP
		if srv.Port != 0 {
			address = fmt.Sprintf("%s:%d", srv.IP, srv.Port)
		}
		d.field("address", address)
	}
	d.field("ssh user", srv.User)
	d.field("proxy", srv.ProxyType)
	if srv.UnreachableCount > 0 {
		d.styledField("failed checks", fmt.Sprintf("%d", srv.UnreachableCount), s.Warning)
	}

	d.section("Configuration")
	d.styledField("reachable", yesNo(srv.Settings.IsReachable),
		boolStyle(s, srv.Settings.IsReachable))
	d.styledField("usable", yesNo(srv.Settings.IsUsable),
		boolStyle(s, srv.Settings.IsUsable))
	if srv.Settings.ForceDisabled {
		d.styledField("disabled", "yes — Coolify has taken this server out of service", s.Danger)
	}
	if srv.Settings.ConcurrentBuilds > 0 {
		d.field("concurrent builds", fmt.Sprintf("%d", srv.Settings.ConcurrentBuilds))
	}
	if srv.Settings.IsBuildServer {
		d.field("role", "build server")
	}
	switch {
	case srv.Settings.IsSwarmManager:
		d.field("swarm", "manager")
	case srv.Settings.IsSwarmWorker:
		d.field("swarm", "worker")
	}
	d.field("wildcard domain", srv.Settings.WildcardDomain)
	d.styledField("metrics", yesNo(srv.Settings.IsMetricsEnabled),
		boolStyle(s, srv.Settings.IsMetricsEnabled))

	// Roll up the applications on this server.
	apps := m.inv.AppsByServer()[srv.UUID]
	var running, degraded, stopped int
	for _, app := range apps {
		st := coolify.ParseStatus(app.Status)
		switch {
		case st.Degraded():
			degraded++
		case st.Running():
			running++
		default:
			stopped++
		}
	}
	d.section(fmt.Sprintf("Applications (%d)", len(apps)))
	if len(apps) == 0 {
		d.raw("  " + s.Faint.Render("none deployed here"))
	} else {
		summary := []string{s.Success.Render(fmt.Sprintf("%d running", running))}
		if degraded > 0 {
			summary = append(summary, s.Warning.Render(fmt.Sprintf("%d degraded", degraded)))
		}
		if stopped > 0 {
			summary = append(summary, s.Danger.Render(fmt.Sprintf("%d stopped", stopped)))
		}
		d.raw("  " + strings.Join(summary, s.Faint.Render(" · ")))
		d.blank()
		for _, app := range apps {
			d.raw("  " + s.StatusDot(app.Status) + " " + s.Value.Render(app.Name))
		}
	}

	d.section("Metadata")
	d.field("uuid", srv.UUID)
	return d.String()
}

// renderPlaceholder is shown for tabs whose content arrives in a later phase.
func (m Dashboard) renderPlaceholder(title, note string) string {
	s := m.styles
	return strings.Join([]string{
		"",
		"  " + s.Bold.Render(title),
		"",
		"  " + s.Faint.Render(note),
	}, "\n")
}

func (m Dashboard) serverByUUID(uuid string) (coolify.Server, bool) {
	for _, srv := range m.inv.Servers {
		if srv.UUID == uuid {
			return srv, true
		}
	}
	return coolify.Server{}, false
}

func (m Dashboard) serverHealthStyle(h coolify.ServerHealth) lipgloss.Style {
	switch h {
	case coolify.ServerHealthy:
		return m.styles.Success
	case coolify.ServerUnreachable:
		return m.styles.Danger
	case coolify.ServerUnusable:
		return m.styles.Warning
	default:
		return m.styles.Faint
	}
}

func boolStyle(s Styles, ok bool) lipgloss.Style {
	if ok {
		return s.Success
	}
	return s.Danger
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// formatDuration renders a duration compactly: 45s, 3m12s, 1h04m.
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// formatRelative renders a timestamp as "3m ago", falling back to a date for
// anything older than a week.
func formatRelative(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	elapsed := time.Since(t)
	switch {
	case elapsed < 0:
		return t.Local().Format("2006-01-02 15:04")
	case elapsed < 10*time.Second:
		return "just now"
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds ago", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	case elapsed < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(elapsed.Hours()/24))
	default:
		return t.Local().Format("2006-01-02")
	}
}
