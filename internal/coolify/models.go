package coolify

import (
	"strings"
	"time"
)

// Server is a machine managed by a Coolify instance.
type Server struct {
	ID          int    `json:"id"`
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IP          string `json:"ip"`
	User        string `json:"user"`
	Port        int    `json:"port"`
	ProxyType   string `json:"proxy_type"`

	UnreachableCount              int  `json:"unreachable_count"`
	UnreachableNotificationSent   bool `json:"unreachable_notification_sent"`
	HighDiskUsageNotificationSent bool `json:"high_disk_usage_notification_sent"`

	Settings ServerSettings `json:"settings"`
}

// ServerSettings holds the health-relevant subset of Coolify's ServerSetting model.
type ServerSettings struct {
	IsReachable      bool   `json:"is_reachable"`
	IsUsable         bool   `json:"is_usable"`
	IsBuildServer    bool   `json:"is_build_server"`
	IsSwarmManager   bool   `json:"is_swarm_manager"`
	IsSwarmWorker    bool   `json:"is_swarm_worker"`
	IsMetricsEnabled bool   `json:"is_metrics_enabled"`
	ForceDisabled    bool   `json:"force_disabled"`
	ConcurrentBuilds int    `json:"concurrent_builds"`
	WildcardDomain   string `json:"wildcard_domain"`
	ServerID         int    `json:"server_id"`
}

// Health summarises a server into a single reportable state.
func (s Server) Health() ServerHealth {
	switch {
	case s.Settings.ForceDisabled:
		return ServerDisabled
	case !s.Settings.IsReachable:
		return ServerUnreachable
	case !s.Settings.IsUsable:
		return ServerUnusable
	default:
		return ServerHealthy
	}
}

// ServerHealth is the rolled-up state of a server.
type ServerHealth string

const (
	ServerHealthy     ServerHealth = "healthy"
	ServerUnreachable ServerHealth = "unreachable"
	ServerUnusable    ServerHealth = "unusable"
	ServerDisabled    ServerHealth = "disabled"
)

// Resource is a lightweight entry from GET /servers/{uuid}/resources. Coolify
// returns every kind of resource here (applications, databases, services), so
// Type is what distinguishes them.
type Resource struct {
	ID        int    `json:"id"`
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Application is the subset of Coolify's 86-field Application model that a
// monitoring TUI actually needs. Unknown fields are ignored by encoding/json.
type Application struct {
	ID          int    `json:"id"`
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FQDN        string `json:"fqdn"`
	Status      string `json:"status"`

	GitRepository string `json:"git_repository"`
	GitBranch     string `json:"git_branch"`
	GitCommitSHA  string `json:"git_commit_sha"`
	GitFullURL    string `json:"git_full_url"`

	BuildPack               string `json:"build_pack"`
	DockerRegistryImageName string `json:"docker_registry_image_name"`
	DockerRegistryImageTag  string `json:"docker_registry_image_tag"`
	PortsExposes            string `json:"ports_exposes"`
	PortsMappings           string `json:"ports_mappings"`

	HealthCheckEnabled bool   `json:"health_check_enabled"`
	HealthCheckPath    string `json:"health_check_path"`

	LimitsMemory string `json:"limits_memory"`
	LimitsCPUs   string `json:"limits_cpus"`

	EnvironmentID   int    `json:"environment_id"`
	DestinationID   int    `json:"destination_id"`
	DestinationType string `json:"destination_type"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// ServerName and ServerUUID are not part of the API response for
	// /applications; they are filled in by joining against per-server
	// resource listings. See Client.Inventory.
	ServerName string `json:"-"`
	ServerUUID string `json:"-"`
}

// Domains splits the comma-separated fqdn field into individual URLs.
func (a Application) Domains() []string {
	if strings.TrimSpace(a.FQDN) == "" {
		return nil
	}
	parts := strings.Split(a.FQDN, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// PrimaryDomain is the first configured domain, or "" when none is set.
func (a Application) PrimaryDomain() string {
	if d := a.Domains(); len(d) > 0 {
		return d[0]
	}
	return ""
}

// ShortCommit trims a full commit SHA down to the usual 7 characters.
func (a Application) ShortCommit() string {
	if len(a.GitCommitSHA) > 7 {
		return a.GitCommitSHA[:7]
	}
	return a.GitCommitSHA
}

// Deployment is an entry in Coolify's deployment queue.
type Deployment struct {
	ID              int    `json:"id"`
	DeploymentUUID  string `json:"deployment_uuid"`
	ApplicationName string `json:"application_name"`
	ServerName      string `json:"server_name"`
	ServerID        int    `json:"server_id"`
	Status          string `json:"status"`
	Commit          string `json:"commit"`
	CommitMessage   string `json:"commit_message"`
	PullRequestID   int    `json:"pull_request_id"`
	ForceRebuild    bool   `json:"force_rebuild"`
	RestartOnly     bool   `json:"restart_only"`
	Rollback        bool   `json:"rollback"`
	IsWebhook       bool   `json:"is_webhook"`
	IsAPI           bool   `json:"is_api"`
	DeploymentURL   string `json:"deployment_url"`
	Logs            string `json:"logs"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// ApplicationID is a string in the API payload even though it names an
	// integer key.
	ApplicationID string `json:"application_id"`
}

// ShortCommit trims the commit SHA to 7 characters.
func (d Deployment) ShortCommit() string {
	if len(d.Commit) > 7 {
		return d.Commit[:7]
	}
	return d.Commit
}

// InProgress reports whether the deployment is still doing work, and so is
// worth polling.
func (d Deployment) InProgress() bool {
	switch strings.ToLower(d.Status) {
	case "in_progress", "queued", "running":
		return true
	}
	return false
}

// Duration is how long the deployment ran (or has been running).
func (d Deployment) Duration() time.Duration {
	if d.CreatedAt.IsZero() {
		return 0
	}
	end := d.UpdatedAt
	if d.InProgress() || end.Before(d.CreatedAt) {
		end = time.Now()
	}
	return end.Sub(d.CreatedAt).Truncate(time.Second)
}

// EnvVar is an application environment variable. Value is only populated when
// the API token carries the read:sensitive permission.
type EnvVar struct {
	ID          int    `json:"id"`
	UUID        string `json:"uuid"`
	Key         string `json:"key"`
	Value       string `json:"value"`
	RealValue   string `json:"real_value"`
	Comment     string `json:"comment"`
	IsBuildTime bool   `json:"is_buildtime"`
	IsRuntime   bool   `json:"is_runtime"`
	IsLiteral   bool   `json:"is_literal"`
	IsMultiline bool   `json:"is_multiline"`
	IsPreview   bool   `json:"is_preview"`
	IsShared    bool   `json:"is_shared"`
	IsShownOnce bool   `json:"is_shown_once"`
}

// Resolved returns the effective value, preferring real_value (which has
// shared-variable references expanded) when present.
func (e EnvVar) Resolved() string {
	if e.RealValue != "" {
		return e.RealValue
	}
	return e.Value
}

// Project groups resources inside a Coolify instance.
type Project struct {
	ID          int    `json:"id"`
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Team is a Coolify team; the API token is scoped to one.
type Team struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ActionResult is the common response to lifecycle and deploy calls.
type ActionResult struct {
	Message        string `json:"message"`
	DeploymentUUID string `json:"deployment_uuid"`
	ResourceUUID   string `json:"resource_uuid"`
}

// DeployResponse wraps the per-resource results of POST /deploy.
type DeployResponse struct {
	Deployments []ActionResult `json:"deployments"`
}

// Status is a parsed Coolify resource status. Coolify reports status as
// "state:health", e.g. "running:healthy", "exited:unhealthy", or sometimes
// just "running" with no health suffix.
type Status struct {
	State  string
	Health string
	Raw    string
}

// ParseStatus splits a raw Coolify status string into state and health.
func ParseStatus(raw string) Status {
	s := Status{Raw: raw}
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		s.State = "unknown"
		return s
	}
	state, health, found := strings.Cut(trimmed, ":")
	s.State = strings.TrimSpace(state)
	if found {
		s.Health = strings.TrimSpace(health)
	}
	return s
}

// Running reports whether the container is up, regardless of health.
func (s Status) Running() bool {
	switch s.State {
	case "running", "starting", "restarting":
		return true
	}
	return false
}

// Degraded reports whether the resource is up but failing its health check.
func (s Status) Degraded() bool {
	return s.Running() && s.Health == "unhealthy"
}

// Label renders a compact human-readable status.
func (s Status) Label() string {
	if s.State == "" || s.State == "unknown" {
		return "unknown"
	}
	if s.Health == "" || s.Health == "unknown" {
		return s.State
	}
	return s.State + ":" + s.Health
}
