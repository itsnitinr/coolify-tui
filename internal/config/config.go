// Package config loads and saves coolify-tui's on-disk configuration.
//
// Security notes:
//   - The config file is written with 0600 permissions inside a 0700 directory.
//   - A token may be kept out of the file entirely by setting token_env to the
//     name of an environment variable holding it.
//   - Tokens are only ever rendered through RedactedToken, and no error message
//     in this package includes a token value.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// AppName is the config directory name under $XDG_CONFIG_HOME.
	AppName = "coolify-tui"
	// FileName is the config file name.
	FileName = "config.yaml"

	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600

	// DefaultRefreshInterval is how often the TUI polls for status changes.
	DefaultRefreshInterval = 10 * time.Second
	// DefaultLogLines is how many log lines to request by default.
	DefaultLogLines = 500
)

// Config is the root of the config file.
type Config struct {
	// ActiveInstance is the name of the instance selected at startup.
	ActiveInstance string `yaml:"active_instance,omitempty"`
	// RefreshInterval controls background polling. Zero means the default.
	RefreshInterval Duration `yaml:"refresh_interval,omitempty"`
	// LogLines is how many container log lines to fetch.
	LogLines int `yaml:"log_lines,omitempty"`
	// ConfirmDestructive requires a confirmation prompt for stop and restart.
	ConfirmDestructive *bool `yaml:"confirm_destructive,omitempty"`
	// Theme selects a colour scheme by name.
	Theme string `yaml:"theme,omitempty"`
	// Instances are the configured Coolify instances.
	Instances []Instance `yaml:"instances"`

	// path records where this config was loaded from, so Save can round-trip.
	path string
	// looseMode records that the file had permissions wider than 0600.
	looseMode os.FileMode
}

// Instance is a single Coolify installation.
type Instance struct {
	// Name is the label shown in the UI and used by --instance.
	Name string `yaml:"name"`
	// URL is the instance root or API base; both forms are accepted.
	URL string `yaml:"url"`
	// Token is the API token, "{id}|{secret}". Prefer TokenEnv on shared
	// machines.
	Token string `yaml:"token,omitempty"`
	// TokenEnv names an environment variable holding the token. It takes
	// precedence over Token.
	TokenEnv string `yaml:"token_env,omitempty"`
	// InsecureSkipVerify disables TLS verification, for self-signed certs.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify,omitempty"`
}

// ErrNoConfig is returned by Load when no config file exists yet, which is the
// signal to run onboarding.
var ErrNoConfig = errors.New("config: no configuration file found")

// Dir returns the directory holding the config file, honouring
// $COOLIFY_TUI_CONFIG_DIR and then $XDG_CONFIG_HOME.
func Dir() (string, error) {
	if custom := os.Getenv("COOLIFY_TUI_CONFIG_DIR"); custom != "" {
		return custom, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, AppName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", AppName), nil
}

// Path returns the full path to the config file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the config file. It returns ErrNoConfig when the file is absent.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a config file from an explicit path.
func LoadFrom(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoConfig
		}
		return nil, fmt.Errorf("config: stat %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.path = path
	if mode := info.Mode().Perm(); modeIsLoose(mode) {
		cfg.looseMode = mode
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = Duration(DefaultRefreshInterval)
	}
	if c.LogLines <= 0 {
		c.LogLines = DefaultLogLines
	}
	if c.ActiveInstance == "" && len(c.Instances) > 0 {
		c.ActiveInstance = c.Instances[0].Name
	}
}

// New returns an empty config with defaults applied, ready for onboarding.
func New() *Config {
	cfg := &Config{}
	cfg.applyDefaults()
	return cfg
}

// Path returns where the config lives on disk, resolving the default location
// when the config was constructed in memory.
func (c *Config) Path() string {
	if c.path != "" {
		return c.path
	}
	if p, err := Path(); err == nil {
		return p
	}
	return FileName
}

// PermissionsEnforced reports whether this platform's file mode bits actually
// govern access, and so whether PermissionWarning can say anything meaningful.
// It is false on Windows, where ACLs do the work instead.
func PermissionsEnforced() bool { return permissionsEnforced }

// PermissionWarning describes over-permissive file modes, or "" when the file
// is correctly locked down. The TUI surfaces this so a world-readable token
// does not go unnoticed. It is always "" where PermissionsEnforced is false.
func (c *Config) PermissionWarning() string {
	if c.looseMode == 0 {
		return ""
	}
	return fmt.Sprintf("%s is mode %#o; it holds API tokens. Run: chmod 600 %s",
		c.Path(), c.looseMode, c.Path())
}

// ShouldConfirmDestructive reports whether stop and restart need confirmation.
// It defaults to true.
func (c *Config) ShouldConfirmDestructive() bool {
	if c.ConfirmDestructive == nil {
		return true
	}
	return *c.ConfirmDestructive
}

// Save writes the config to its path, creating parent directories, with
// restrictive permissions. The write is atomic: a temp file in the same
// directory is renamed into place.
func (c *Config) Save() error {
	path := c.Path()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("config: create %s: %w", dir, err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	header := "# coolify-tui configuration\n" +
		"# This file can contain API tokens: keep it at mode 0600 and out of git.\n" +
		"# To keep a token out of this file, drop `token` and set:\n" +
		"#   token_env: MY_COOLIFY_TOKEN\n"
	data = append([]byte(header), data...)

	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("config: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(filePerm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: install %s: %w", path, err)
	}
	c.path = path
	c.looseMode = 0
	return nil
}

// Instance returns the instance with the given name.
func (c *Config) Instance(name string) (Instance, bool) {
	for _, inst := range c.Instances {
		if strings.EqualFold(inst.Name, name) {
			return inst, true
		}
	}
	return Instance{}, false
}

// Active returns the currently selected instance.
func (c *Config) Active() (Instance, bool) {
	if c.ActiveInstance != "" {
		if inst, ok := c.Instance(c.ActiveInstance); ok {
			return inst, true
		}
	}
	if len(c.Instances) > 0 {
		return c.Instances[0], true
	}
	return Instance{}, false
}

// SetActive selects an instance by name.
func (c *Config) SetActive(name string) error {
	if _, ok := c.Instance(name); !ok {
		return fmt.Errorf("config: no instance named %q", name)
	}
	c.ActiveInstance = name
	return nil
}

// Upsert adds an instance, replacing any existing one with the same name.
func (c *Config) Upsert(inst Instance) error {
	if err := inst.Validate(); err != nil {
		return err
	}
	for i, existing := range c.Instances {
		if strings.EqualFold(existing.Name, inst.Name) {
			c.Instances[i] = inst
			return nil
		}
	}
	c.Instances = append(c.Instances, inst)
	if c.ActiveInstance == "" {
		c.ActiveInstance = inst.Name
	}
	return nil
}

// Remove deletes an instance by name.
func (c *Config) Remove(name string) error {
	for i, existing := range c.Instances {
		if !strings.EqualFold(existing.Name, name) {
			continue
		}
		c.Instances = append(c.Instances[:i], c.Instances[i+1:]...)
		if strings.EqualFold(c.ActiveInstance, name) {
			c.ActiveInstance = ""
			if len(c.Instances) > 0 {
				c.ActiveInstance = c.Instances[0].Name
			}
		}
		return nil
	}
	return fmt.Errorf("config: no instance named %q", name)
}

// Names lists the configured instance names in order.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Instances))
	for _, inst := range c.Instances {
		names = append(names, inst.Name)
	}
	return names
}

// Validate checks that an instance is usable.
func (i Instance) Validate() error {
	if strings.TrimSpace(i.Name) == "" {
		return errors.New("config: instance name is required")
	}
	if strings.TrimSpace(i.URL) == "" {
		return errors.New("config: instance URL is required")
	}
	if i.Token == "" && i.TokenEnv == "" {
		return errors.New("config: instance needs either token or token_env")
	}
	return nil
}

// ResolveToken returns the effective token for the instance, reading TokenEnv
// from the environment when it is set.
func (i Instance) ResolveToken() (string, error) {
	if i.TokenEnv != "" {
		token := strings.TrimSpace(os.Getenv(i.TokenEnv))
		if token == "" {
			return "", fmt.Errorf("config: instance %q expects its token in $%s, which is unset or empty",
				i.Name, i.TokenEnv)
		}
		return token, nil
	}
	if token := strings.TrimSpace(i.Token); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("config: instance %q has no token configured", i.Name)
}

// RedactedToken renders a token safe for display: the numeric ID stays visible
// because it is not secret, the secret half is masked.
func RedactedToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "(unset)"
	}
	id, secret, found := strings.Cut(token, "|")
	if !found {
		return strings.Repeat("*", min(len(token), 12))
	}
	masked := strings.Repeat("*", min(max(len(secret), 4), 12))
	return id + "|" + masked
}
