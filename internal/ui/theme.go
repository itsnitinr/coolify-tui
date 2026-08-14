// Package ui holds the terminal interface: the shared theme, the onboarding
// wizard and the dashboard.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// Theme is the colour palette. Every colour is adaptive so the UI stays legible
// on both light and dark terminals.
type Theme struct {
	Name string

	Accent    lipgloss.AdaptiveColor
	AccentDim lipgloss.AdaptiveColor
	Success   lipgloss.AdaptiveColor
	Warning   lipgloss.AdaptiveColor
	Danger    lipgloss.AdaptiveColor
	Info      lipgloss.AdaptiveColor

	Text     lipgloss.AdaptiveColor
	Muted    lipgloss.AdaptiveColor
	Faint    lipgloss.AdaptiveColor
	Border   lipgloss.AdaptiveColor
	BorderHi lipgloss.AdaptiveColor

	SelectionBg lipgloss.AdaptiveColor
	SelectionFg lipgloss.AdaptiveColor
}

// DefaultTheme is a violet scheme that nods to Coolify's own palette.
func DefaultTheme() Theme {
	return Theme{
		Name:      "coolify",
		Accent:    lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#A78BFA"},
		AccentDim: lipgloss.AdaptiveColor{Light: "#A78BFA", Dark: "#6D28D9"},
		Success:   lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"},
		Warning:   lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"},
		Danger:    lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"},
		Info:      lipgloss.AdaptiveColor{Light: "#0369A1", Dark: "#38BDF8"},

		Text:     lipgloss.AdaptiveColor{Light: "#18181B", Dark: "#E4E4E7"},
		Muted:    lipgloss.AdaptiveColor{Light: "#52525B", Dark: "#A1A1AA"},
		Faint:    lipgloss.AdaptiveColor{Light: "#A1A1AA", Dark: "#52525B"},
		Border:   lipgloss.AdaptiveColor{Light: "#D4D4D8", Dark: "#3F3F46"},
		BorderHi: lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#A78BFA"},

		SelectionBg: lipgloss.AdaptiveColor{Light: "#EDE9FE", Dark: "#4C1D95"},
		SelectionFg: lipgloss.AdaptiveColor{Light: "#4C1D95", Dark: "#F5F3FF"},
	}
}

// MonoTheme is a colour-light scheme for terminals where the violet clashes.
func MonoTheme() Theme {
	t := DefaultTheme()
	t.Name = "mono"
	t.Accent = lipgloss.AdaptiveColor{Light: "#18181B", Dark: "#FAFAFA"}
	t.AccentDim = lipgloss.AdaptiveColor{Light: "#52525B", Dark: "#A1A1AA"}
	t.BorderHi = t.Accent
	t.SelectionBg = lipgloss.AdaptiveColor{Light: "#E4E4E7", Dark: "#3F3F46"}
	t.SelectionFg = lipgloss.AdaptiveColor{Light: "#18181B", Dark: "#FAFAFA"}
	return t
}

// ThemeByName resolves a theme name from config, falling back to the default.
func ThemeByName(name string) Theme {
	switch name {
	case "mono", "monochrome":
		return MonoTheme()
	default:
		return DefaultTheme()
	}
}

// Styles are the reusable lipgloss styles derived from a Theme. Building them
// once per theme keeps render paths free of style construction.
type Styles struct {
	Theme Theme

	// Text
	Title    lipgloss.Style
	Subtitle lipgloss.Style
	Label    lipgloss.Style
	Value    lipgloss.Style
	Muted    lipgloss.Style
	Faint    lipgloss.Style
	Bold     lipgloss.Style
	Accent   lipgloss.Style
	Link     lipgloss.Style

	// Semantic
	Success lipgloss.Style
	Warning lipgloss.Style
	Danger  lipgloss.Style
	Info    lipgloss.Style

	// Chrome
	Panel        lipgloss.Style
	PanelFocused lipgloss.Style
	PanelTitle   lipgloss.Style
	StatusBar    lipgloss.Style
	HelpBar      lipgloss.Style
	Selected     lipgloss.Style
	Cursor       lipgloss.Style
	Badge        lipgloss.Style
	Modal        lipgloss.Style
	Toast        lipgloss.Style
	ToastError   lipgloss.Style
	Spinner      lipgloss.Style
}

// NewStyles builds the style set for a theme.
func NewStyles(t Theme) Styles {
	base := lipgloss.NewStyle()
	return Styles{
		Theme: t,

		Title:    base.Foreground(t.Accent).Bold(true),
		Subtitle: base.Foreground(t.Muted),
		Label:    base.Foreground(t.Muted),
		Value:    base.Foreground(t.Text),
		Muted:    base.Foreground(t.Muted),
		Faint:    base.Foreground(t.Faint),
		Bold:     base.Foreground(t.Text).Bold(true),
		Accent:   base.Foreground(t.Accent),
		Link:     base.Foreground(t.Info).Underline(true),

		Success: base.Foreground(t.Success),
		Warning: base.Foreground(t.Warning),
		Danger:  base.Foreground(t.Danger),
		Info:    base.Foreground(t.Info),

		Panel: base.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Border),
		PanelFocused: base.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.BorderHi),
		PanelTitle: base.Foreground(t.Accent).Bold(true).Padding(0, 1),
		StatusBar:  base.Foreground(t.Muted),
		HelpBar:    base.Foreground(t.Faint),
		Selected: base.
			Foreground(t.SelectionFg).
			Background(t.SelectionBg).
			Bold(true),
		Cursor: base.Foreground(t.Accent).Bold(true),
		Badge:  base.Foreground(t.SelectionFg).Background(t.SelectionBg).Padding(0, 1),
		Modal: base.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.BorderHi).
			Padding(1, 3),
		Toast: base.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Success).
			Foreground(t.Text).
			Padding(0, 2),
		ToastError: base.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.Danger).
			Foreground(t.Text).
			Padding(0, 2),
		Spinner: base.Foreground(t.Accent),
	}
}

// StatusIndicator renders a coloured dot plus label for an application status.
func (s Styles) StatusIndicator(raw string) string {
	st := coolify.ParseStatus(raw)
	return s.statusDot(st) + " " + s.statusText(st).Render(st.Label())
}

// StatusDot renders just the coloured dot for an application status, for use in
// dense lists.
func (s Styles) StatusDot(raw string) string {
	return s.statusDot(coolify.ParseStatus(raw))
}

func (s Styles) statusDot(st coolify.Status) string {
	switch {
	case st.Degraded():
		return s.Warning.Render("◍")
	case st.Running():
		return s.Success.Render("●")
	case st.State == "unknown" || st.State == "":
		return s.Faint.Render("○")
	case st.State == "restarting" || st.State == "starting":
		return s.Info.Render("◐")
	default:
		return s.Danger.Render("○")
	}
}

func (s Styles) statusText(st coolify.Status) lipgloss.Style {
	switch {
	case st.Degraded():
		return s.Warning
	case st.Running():
		return s.Success
	case st.State == "unknown" || st.State == "":
		return s.Faint
	default:
		return s.Danger
	}
}

// ServerIndicator renders a coloured dot plus label for a server's health.
func (s Styles) ServerIndicator(h coolify.ServerHealth) string {
	switch h {
	case coolify.ServerHealthy:
		return s.Success.Render("● healthy")
	case coolify.ServerUnreachable:
		return s.Danger.Render("✕ unreachable")
	case coolify.ServerUnusable:
		return s.Warning.Render("◍ unusable")
	case coolify.ServerDisabled:
		return s.Faint.Render("○ disabled")
	default:
		return s.Faint.Render("○ unknown")
	}
}

// ServerDot renders just the health dot for a server.
func (s Styles) ServerDot(h coolify.ServerHealth) string {
	switch h {
	case coolify.ServerHealthy:
		return s.Success.Render("●")
	case coolify.ServerUnreachable:
		return s.Danger.Render("✕")
	case coolify.ServerUnusable:
		return s.Warning.Render("◍")
	default:
		return s.Faint.Render("○")
	}
}

// DeploymentIndicator renders a deployment status with an appropriate glyph.
func (s Styles) DeploymentIndicator(status string) string {
	return s.deploymentStyle(status).Render(plainDeploymentGlyph(status) + " " + statusOrUnknown(status))
}

// DeploymentGlyph renders just the coloured glyph for a deployment status.
func (s Styles) DeploymentGlyph(status string) string {
	return s.deploymentStyle(status).Render(plainDeploymentGlyph(status))
}

func (s Styles) deploymentStyle(status string) lipgloss.Style {
	switch strings.ToLower(status) {
	case "finished", "success":
		return s.Success
	case "failed", "error":
		return s.Danger
	case "in_progress", "running":
		return s.Info
	case "queued":
		return s.Muted
	case "cancelled_by_user", "cancelled":
		return s.Warning
	default:
		return s.Muted
	}
}

func statusOrUnknown(status string) string {
	if strings.TrimSpace(status) == "" {
		return "unknown"
	}
	return status
}
