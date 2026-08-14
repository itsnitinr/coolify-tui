package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/itsnitinr/coolify-tui/internal/config"
	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// onboardStep is the wizard's position.
type onboardStep int

const (
	stepIntro onboardStep = iota
	stepName
	stepURL
	stepToken
	stepVerifying
	stepResult
)

// field indexes into the wizard's inputs slice.
const (
	fieldName = iota
	fieldURL
	fieldToken
	fieldCount
)

// OnboardResult reports what the wizard produced.
type OnboardResult struct {
	// Instance is the instance that was configured and saved.
	Instance config.Instance
	// Saved is true when the config file was written.
	Saved bool
	// Cancelled is true when the user quit before finishing.
	Cancelled bool
}

// probeResult carries the outcome of validating credentials.
type probeResult struct {
	version     string
	teams       []coolify.Team
	servers     int
	apps        int
	permissions []permissionCheck
	err         error
}

// permissionCheck records whether one token permission is present.
type permissionCheck struct {
	name     string
	scope    string
	required bool
	ok       bool
	detail   string
}

type probeMsg probeResult

type savedMsg struct{ err error }

// OnboardModel is the first-run wizard: it collects an instance name, URL and
// API token, verifies them against the live instance, reports which token
// permissions are present, and saves the config.
type OnboardModel struct {
	cfg    *config.Config
	styles Styles

	step    onboardStep
	inputs  []textinput.Model
	focus   int
	spin    spinner.Model
	probe   probeResult
	saveErr error

	// editing is true when the wizard is adding an instance to an existing
	// config rather than running as first-run onboarding.
	editing bool

	width  int
	height int

	result OnboardResult
}

// NewOnboardModel builds the wizard. Pass an existing config to add another
// instance to it, or config.New() for first-run onboarding.
func NewOnboardModel(cfg *config.Config, styles Styles, addingToExisting bool) OnboardModel {
	inputs := make([]textinput.Model, fieldCount)

	name := textinput.New()
	name.Placeholder = "prod"
	name.CharLimit = 40
	name.Width = 44
	name.Prompt = ""
	inputs[fieldName] = name

	instURL := textinput.New()
	instURL.Placeholder = "coolify.example.com"
	instURL.CharLimit = 300
	instURL.Width = 44
	instURL.Prompt = ""
	inputs[fieldURL] = instURL

	token := textinput.New()
	token.Placeholder = "42|xxxxxxxxxxxxxxxxxxxxxxxx"
	token.CharLimit = 400
	token.Width = 44
	token.Prompt = ""
	// Mask the token: it must never be readable on screen or in a screenshot.
	token.EchoMode = textinput.EchoPassword
	token.EchoCharacter = '•'
	inputs[fieldToken] = token

	for i := range inputs {
		inputs[i].PromptStyle = styles.Accent
		inputs[i].TextStyle = styles.Value
		inputs[i].PlaceholderStyle = styles.Faint
	}

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.Spinner

	return OnboardModel{
		cfg:     cfg,
		styles:  styles,
		step:    stepIntro,
		inputs:  inputs,
		spin:    sp,
		editing: addingToExisting,
	}
}

// Result reports what the wizard produced. Read it after tea.Program.Run.
func (m OnboardModel) Result() OnboardResult { return m.result }

// Init implements tea.Model.
func (m OnboardModel) Init() tea.Cmd { return textinput.Blink }

// Update implements tea.Model.
func (m OnboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		if m.step != stepVerifying {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case probeMsg:
		m.probe = probeResult(msg)
		m.step = stepResult
		if m.probe.err != nil {
			return m, nil
		}
		// Credentials are good: persist them.
		return m, m.save()

	case savedMsg:
		m.saveErr = msg.err
		if msg.err == nil {
			m.result.Saved = true
			m.result.Instance = m.instance()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m OnboardModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.result.Cancelled = !m.result.Saved
		return m, tea.Quit
	case "esc":
		// Esc backs out one step, or quits from the first.
		switch m.step {
		case stepIntro:
			m.result.Cancelled = true
			return m, tea.Quit
		case stepName:
			m.step = stepIntro
			return m, m.blurAll()
		case stepURL:
			return m.focusStep(stepName)
		case stepToken:
			return m.focusStep(stepURL)
		case stepResult:
			if m.probe.err != nil || m.saveErr != nil {
				return m.focusStep(stepToken)
			}
		}
		return m, nil
	}

	switch m.step {
	case stepIntro:
		switch msg.String() {
		case "enter", " ":
			return m.focusStep(stepName)
		case "q":
			m.result.Cancelled = true
			return m, tea.Quit
		}
		return m, nil

	case stepName, stepURL, stepToken:
		switch msg.String() {
		case "enter":
			return m.advance()
		case "tab", "shift+tab", "down", "up":
			return m.cycle(msg.String())
		}
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd

	case stepVerifying:
		return m, nil

	case stepResult:
		switch msg.String() {
		case "enter", "q":
			if m.probe.err != nil || m.saveErr != nil {
				// Let the user fix the input rather than quitting silently.
				return m.focusStep(stepToken)
			}
			return m, tea.Quit
		case "r":
			return m.startProbe()
		}
		return m, nil
	}
	return m, nil
}

// advance moves to the next field, validating the current one first.
func (m OnboardModel) advance() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepName:
		if strings.TrimSpace(m.inputs[fieldName].Value()) == "" {
			// Offer a sensible default rather than blocking on an empty name.
			m.inputs[fieldName].SetValue("default")
		}
		return m.focusStep(stepURL)
	case stepURL:
		if strings.TrimSpace(m.inputs[fieldURL].Value()) == "" {
			return m, nil
		}
		return m.focusStep(stepToken)
	case stepToken:
		if strings.TrimSpace(m.inputs[fieldToken].Value()) == "" {
			return m, nil
		}
		return m.startProbe()
	}
	return m, nil
}

// cycle moves focus between the three fields without leaving the form.
func (m OnboardModel) cycle(key string) (tea.Model, tea.Cmd) {
	delta := 1
	if key == "shift+tab" || key == "up" {
		delta = -1
	}
	next := m.focus + delta
	if next < 0 {
		next = 0
	}
	if next >= fieldCount {
		next = fieldCount - 1
	}
	return m.focusStep(stepName + onboardStep(next))
}

// focusStep moves to a form step and focuses its input.
func (m OnboardModel) focusStep(step onboardStep) (tea.Model, tea.Cmd) {
	m.step = step
	m.focus = int(step - stepName)
	if m.focus < 0 || m.focus >= fieldCount {
		m.focus = 0
	}
	cmds := make([]tea.Cmd, 0, fieldCount)
	for i := range m.inputs {
		if i == m.focus {
			cmds = append(cmds, m.inputs[i].Focus())
			continue
		}
		m.inputs[i].Blur()
	}
	return m, tea.Batch(cmds...)
}

func (m OnboardModel) blurAll() tea.Cmd {
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	return nil
}

// instance builds a config.Instance from the current field values.
func (m OnboardModel) instance() config.Instance {
	return config.Instance{
		Name:  strings.TrimSpace(m.inputs[fieldName].Value()),
		URL:   strings.TrimSpace(m.inputs[fieldURL].Value()),
		Token: strings.TrimSpace(m.inputs[fieldToken].Value()),
	}
}

// startProbe kicks off credential verification.
func (m OnboardModel) startProbe() (tea.Model, tea.Cmd) {
	m.step = stepVerifying
	m.probe = probeResult{}
	m.saveErr = nil
	_ = m.blurAll()
	return m, tea.Batch(m.spin.Tick, probeInstance(m.instance()))
}

// probeInstance verifies credentials and discovers which permissions the token
// carries, so the wizard can warn about a read-only token up front rather than
// failing on the first deploy.
func probeInstance(inst config.Instance) tea.Cmd {
	return func() tea.Msg {
		result := probeResult{}

		client, err := coolify.New(inst.URL, inst.Token,
			coolify.WithInsecureSkipVerify(inst.InsecureSkipVerify),
			coolify.WithTimeout(15*time.Second),
		)
		if err != nil {
			result.err = err
			return probeMsg(result)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		version, err := client.Version(ctx)
		if err != nil {
			result.err = describeProbeError(inst, err)
			return probeMsg(result)
		}
		result.version = version

		// read: the dashboard cannot function without it.
		servers, serversErr := client.Servers(ctx)
		readCheck := permissionCheck{name: "read", scope: "servers, applications", required: true}
		if serversErr != nil {
			readCheck.detail = serversErr.Error()
		} else {
			readCheck.ok = true
			result.servers = len(servers)
			if apps, err := client.Applications(ctx); err == nil {
				result.apps = len(apps)
				readCheck.detail = fmt.Sprintf("%d servers, %d applications", len(servers), len(apps))
			} else {
				readCheck.detail = fmt.Sprintf("%d servers", len(servers))
			}
		}
		result.permissions = append(result.permissions, readCheck)

		if teams, err := client.Teams(ctx); err == nil {
			result.teams = teams
		}

		// read:sensitive: needed for container logs and env vars. Probe it
		// against a real application, since there is no capability endpoint.
		sensitive := permissionCheck{name: "read:sensitive", scope: "container logs, env vars"}
		if apps, err := client.Applications(ctx); err == nil && len(apps) > 0 {
			if _, err := client.ApplicationEnvs(ctx, apps[0].UUID); err == nil {
				sensitive.ok = true
				sensitive.detail = "environment variables readable"
			} else if coolify.IsUnauthorized(err) {
				sensitive.detail = "not granted — logs and env vars will be unavailable"
			} else {
				// Some other failure; do not claim the permission is missing.
				sensitive.detail = "could not determine: " + err.Error()
			}
		} else {
			sensitive.detail = "no applications yet, cannot probe"
		}
		result.permissions = append(result.permissions, sensitive)

		// write and deploy cannot be probed without causing a side effect, so
		// they are reported as unverifiable rather than guessed at.
		result.permissions = append(result.permissions,
			permissionCheck{
				name:   "write",
				scope:  "start, stop, restart",
				detail: "not probed (would change state)",
			},
			permissionCheck{
				name:   "deploy",
				scope:  "triggering deployments",
				detail: "not probed (would start a build)",
			},
		)

		if serversErr != nil {
			result.err = fmt.Errorf("token authenticated but cannot list servers: %w", serversErr)
		}
		return probeMsg(result)
	}
}

// describeProbeError turns a connection failure into something actionable.
func describeProbeError(inst config.Instance, err error) error {
	if coolify.IsUnauthorized(err) {
		return fmt.Errorf("%w\n\nThe URL resolved but the token was rejected. Create a new "+
			"token under Security → API Tokens and copy it exactly, including the \"42|\" prefix.", err)
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "certificate"):
		return fmt.Errorf("%w\n\nThis looks like a TLS certificate problem. For a self-signed "+
			"homelab instance, add `insecure_skip_verify: true` to this instance in the config file.", err)
	case strings.Contains(msg, "no such host"):
		return fmt.Errorf("%w\n\nThe hostname did not resolve. Check the spelling of %q.", err, inst.URL)
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("%w\n\nNothing is listening there. If Coolify runs on a non-standard "+
			"port, include it, e.g. http://192.168.1.10:8000.", err)
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return fmt.Errorf("%w\n\nThe request timed out. Check the instance is up and reachable "+
			"from this machine (a firewall or VPN may be in the way).", err)
	case strings.Contains(msg, "404"):
		return fmt.Errorf("%w\n\nThe host answered but has no Coolify API at /api/v1. Point the "+
			"URL at the Coolify dashboard root.", err)
	}
	return err
}

// save writes the verified instance into the config file.
func (m OnboardModel) save() tea.Cmd {
	inst := m.instance()
	cfg := m.cfg
	return func() tea.Msg {
		if err := cfg.Upsert(inst); err != nil {
			return savedMsg{err: err}
		}
		if err := cfg.SetActive(inst.Name); err != nil {
			return savedMsg{err: err}
		}
		return savedMsg{err: cfg.Save()}
	}
}

// View implements tea.Model.
func (m OnboardModel) View() string {
	var body string
	switch m.step {
	case stepIntro:
		body = m.viewIntro()
	case stepName, stepURL, stepToken:
		body = m.viewForm()
	case stepVerifying:
		body = m.viewVerifying()
	case stepResult:
		body = m.viewResult()
	}

	content := lipgloss.JoinVertical(lipgloss.Left, m.viewHeader(), "", body)
	card := m.styles.Modal.Render(content)

	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
	}
	return card
}

func (m OnboardModel) viewHeader() string {
	s := m.styles
	title := s.Title.Render("coolify-tui")
	sub := s.Subtitle.Render("  a terminal dashboard for Coolify")
	return lipgloss.JoinHorizontal(lipgloss.Left, title, sub)
}

func (m OnboardModel) viewIntro() string {
	s := m.styles
	heading := "Let's connect to your Coolify instance."
	if m.editing {
		heading = "Let's add another Coolify instance."
	}

	lines := []string{
		s.Bold.Render(heading),
		"",
		s.Muted.Render("You'll need two things:"),
		"",
		"  " + s.Accent.Render("1.") + " " + s.Value.Render("Your instance URL"),
		"     " + s.Faint.Render("e.g. coolify.example.com or http://192.168.1.10:8000"),
		"",
		"  " + s.Accent.Render("2.") + " " + s.Value.Render("An API token"),
		"     " + s.Faint.Render("Coolify → Security → API Tokens → create one"),
		"     " + s.Faint.Render("Permissions: read, read:sensitive, write, deploy"),
		"     " + s.Faint.Render("(read alone is enough for read-only monitoring)"),
		"",
		s.Muted.Render("The token is stored at mode 0600 in:"),
		"  " + s.Value.Render(m.cfg.Path()),
		s.Faint.Render("  To keep it out of that file entirely, see token_env in the README."),
		"",
		s.HelpBar.Render("enter continue · esc quit"),
	}
	return strings.Join(lines, "\n")
}

func (m OnboardModel) viewForm() string {
	s := m.styles

	field := func(idx int, label, hint string) string {
		focused := m.focus == idx

		marker := "  "
		labelStyle := s.Label
		if focused {
			marker = s.Cursor.Render("▸ ")
			labelStyle = s.Bold
		}

		// MarginLeft rather than a string prefix: the box is multi-line, and a
		// prefix would only indent its first row.
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(s.Theme.Border).
			Padding(0, 1).
			MarginLeft(2)
		if focused {
			box = box.BorderForeground(s.Theme.BorderHi)
		}

		rows := []string{
			marker + labelStyle.Render(label),
			box.Render(m.inputs[idx].View()),
		}
		if hint != "" && focused {
			rows = append(rows, s.Faint.MarginLeft(4).Render(hint))
		}
		return strings.Join(rows, "\n")
	}

	parts := []string{
		field(fieldName, "Name", "a short label for this instance, e.g. prod or homelab"),
		"",
		field(fieldURL, "Instance URL", "scheme optional; https is assumed"),
		"",
		field(fieldToken, "API token", "input is masked; paste with ctrl+v or your terminal's paste"),
		"",
		s.HelpBar.Render("enter next · tab/shift+tab move · esc back · ctrl+c quit"),
	}
	return strings.Join(parts, "\n")
}

func (m OnboardModel) viewVerifying() string {
	s := m.styles
	inst := m.instance()
	return strings.Join([]string{
		m.spin.View() + s.Value.Render(" Verifying credentials…"),
		"",
		s.Label.Render("  instance  ") + s.Value.Render(inst.Name),
		s.Label.Render("  url       ") + s.Value.Render(inst.URL),
		s.Label.Render("  token     ") + s.Faint.Render(config.RedactedToken(inst.Token)),
		"",
		s.Faint.Render("  Checking reachability, then probing token permissions."),
	}, "\n")
}

func (m OnboardModel) viewResult() string {
	s := m.styles
	inst := m.instance()

	if m.probe.err != nil {
		return strings.Join([]string{
			s.Danger.Render("✕ Could not connect"),
			"",
			s.Value.Render(indent(wrap(m.probe.err.Error(), 64), "  ")),
			"",
			s.Label.Render("  url    ") + s.Value.Render(inst.URL),
			s.Label.Render("  token  ") + s.Faint.Render(config.RedactedToken(inst.Token)),
			"",
			s.HelpBar.Render("enter fix details · r retry · ctrl+c quit"),
		}, "\n")
	}

	lines := []string{
		s.Success.Render("✓ Connected to Coolify " + m.probe.version),
		"",
	}

	if len(m.probe.teams) > 0 {
		names := make([]string, 0, len(m.probe.teams))
		for _, t := range m.probe.teams {
			names = append(names, t.Name)
		}
		lines = append(lines,
			s.Label.Render("  team        ")+s.Value.Render(strings.Join(names, ", ")))
	}
	lines = append(lines,
		s.Label.Render("  inventory   ")+s.Value.Render(
			fmt.Sprintf("%d servers, %d applications", m.probe.servers, m.probe.apps)),
		"",
		s.Bold.Render("  Token permissions"),
	)

	for _, p := range m.probe.permissions {
		var glyph string
		switch {
		case p.ok:
			glyph = s.Success.Render("✓")
		case p.required:
			glyph = s.Danger.Render("✕")
		default:
			glyph = s.Faint.Render("·")
		}
		row := fmt.Sprintf("  %s %-16s %s", glyph, p.name, s.Faint.Render(p.detail))
		lines = append(lines, row)
	}

	lines = append(lines, "")
	switch {
	case m.saveErr != nil:
		lines = append(lines,
			s.Danger.Render("✕ Could not save the config"),
			"  "+s.Value.Render(m.saveErr.Error()),
			"",
			s.HelpBar.Render("enter fix details · ctrl+c quit"))
	case m.result.Saved:
		lines = append(lines,
			s.Success.Render("✓ Saved to ")+s.Value.Render(m.cfg.Path())+s.Faint.Render(" (mode 0600)"),
			"",
			s.HelpBar.Render("enter open the dashboard"))
	default:
		lines = append(lines, m.spin.View()+s.Muted.Render(" Saving…"))
	}

	return strings.Join(lines, "\n")
}

// wrap breaks text at width, preserving existing newlines.
func wrap(text string, width int) string {
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		var line string
		for _, word := range strings.Fields(paragraph) {
			switch {
			case line == "":
				line = word
			case len(line)+1+len(word) <= width:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// indent prefixes every line of text.
func indent(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// RunOnboarding shows the wizard and returns its result. It is used both for
// first run and for adding an instance later.
func RunOnboarding(cfg *config.Config, styles Styles, addingToExisting bool) (OnboardResult, error) {
	model := NewOnboardModel(cfg, styles, addingToExisting)
	program := tea.NewProgram(model, tea.WithAltScreen())
	final, err := program.Run()
	if err != nil {
		return OnboardResult{}, err
	}
	done, ok := final.(OnboardModel)
	if !ok {
		return OnboardResult{}, errors.New("onboarding: unexpected final model")
	}
	return done.Result(), nil
}
