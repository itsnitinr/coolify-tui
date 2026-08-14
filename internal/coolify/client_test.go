package coolify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "coolify.example.com", want: "https://coolify.example.com/api/v1"},
		{in: "https://coolify.example.com", want: "https://coolify.example.com/api/v1"},
		{in: "https://coolify.example.com/", want: "https://coolify.example.com/api/v1"},
		{in: "https://coolify.example.com/api/v1", want: "https://coolify.example.com/api/v1"},
		{in: "https://coolify.example.com/api/v1/", want: "https://coolify.example.com/api/v1"},
		{in: "https://coolify.example.com/api", want: "https://coolify.example.com/api/v1"},
		{in: "http://192.168.1.10:8000", want: "http://192.168.1.10:8000/api/v1"},
		{in: "  coolify.example.com  ", want: "https://coolify.example.com/api/v1"},
		{in: "https://coolify.example.com?x=1", want: "https://coolify.example.com/api/v1"},
		{in: "", wantErr: true},
		{in: "ftp://coolify.example.com", wantErr: true},
		{in: "https://", wantErr: true},
	}
	for _, tc := range tests {
		got, err := NormalizeURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeURL(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeURL(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewRequiresToken(t *testing.T) {
	if _, err := New("coolify.example.com", "  "); err == nil {
		t.Fatal("New with empty token: want error, got nil")
	}
}

func TestDashboardURL(t *testing.T) {
	c, err := New("https://coolify.example.com/api/v1", "1|secret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := c.DashboardURL(), "https://coolify.example.com"; got != want {
		t.Errorf("DashboardURL() = %q, want %q", got, want)
	}
}

// newTestClient spins up an httptest server with the given handler and returns
// a client pointed at it.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "42|test-secret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestAuthHeaderAndPaths(t *testing.T) {
	var gotAuth, gotPath, gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":"hello"}`))
	})

	logs, err := c.ApplicationLogs(context.Background(), "app-uuid", 100, true)
	if err != nil {
		t.Fatalf("ApplicationLogs: %v", err)
	}
	if logs != "hello" {
		t.Errorf("logs = %q, want %q", logs, "hello")
	}
	if want := "Bearer 42|test-secret"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if want := "/api/v1/applications/app-uuid/logs"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if want := "lines=100&show_timestamps=true"; gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
}

func TestDeployQuery(t *testing.T) {
	var gotMethod, gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"deployments":[{"message":"queued","resource_uuid":"a","deployment_uuid":"d1"}]}`))
	})

	results, err := c.Deploy(context.Background(), []string{"a", "b"}, true)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "force=true&uuid=a%2Cb"; gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
	if len(results) != 1 || results[0].DeploymentUUID != "d1" {
		t.Errorf("results = %+v, want one entry with deployment_uuid d1", results)
	}
}

func TestDeployRequiresUUID(t *testing.T) {
	c, err := New("coolify.example.com", "1|s")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Deploy(context.Background(), nil, false); err == nil {
		t.Fatal("Deploy with no UUIDs: want error, got nil")
	}
}

func TestAPIErrorMessages(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantSub  string
		wantAuth bool
	}{
		{
			name:     "unauthorized",
			status:   http.StatusUnauthorized,
			body:     `{"message":"Unauthenticated."}`,
			wantSub:  "check the API token",
			wantAuth: true,
		},
		{
			name:     "forbidden",
			status:   http.StatusForbidden,
			body:     `{"message":"You don't have permission."}`,
			wantSub:  "missing a required permission",
			wantAuth: true,
		},
		{
			name:    "rate limited",
			status:  http.StatusTooManyRequests,
			body:    `{"message":"Too Many Attempts."}`,
			wantSub: "rate limited",
		},
		{
			name:    "validation errors",
			status:  http.StatusUnprocessableEntity,
			body:    `{"message":"The given data was invalid.","errors":{"uuid":["required"]}}`,
			wantSub: "uuid: required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.Applications(context.Background())
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if got := StatusCode(err); got != tc.status {
				t.Errorf("StatusCode = %d, want %d", got, tc.status)
			}
			if IsUnauthorized(err) != tc.wantAuth {
				t.Errorf("IsUnauthorized = %v, want %v", IsUnauthorized(err), tc.wantAuth)
			}
			if msg := err.Error(); !strings.Contains(msg, tc.wantSub) {
				t.Errorf("error %q does not contain %q", msg, tc.wantSub)
			}
		})
	}
}

func TestAPIErrorNeverLeaksToken(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	})
	_, err := c.Servers(context.Background())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if strings.Contains(err.Error(), "test-secret") {
		t.Errorf("error message leaks token: %q", err.Error())
	}
}

func TestIsNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Application not found."}`))
	})
	_, err := c.Application(context.Background(), "missing")
	if !IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}
}

func TestVersionReadsPlainText(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/version" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte("4.0.0-beta.397\n"))
	})
	got, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != "4.0.0-beta.397" {
		t.Errorf("Version = %q, want trimmed version string", got)
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		raw      string
		state    string
		health   string
		running  bool
		degraded bool
		label    string
	}{
		{raw: "running:healthy", state: "running", health: "healthy", running: true, label: "running:healthy"},
		{raw: "running:unhealthy", state: "running", health: "unhealthy", running: true, degraded: true, label: "running:unhealthy"},
		{raw: "exited:unhealthy", state: "exited", health: "unhealthy", label: "exited:unhealthy"},
		{raw: "running", state: "running", running: true, label: "running"},
		{raw: "RUNNING:HEALTHY", state: "running", health: "healthy", running: true, label: "running:healthy"},
		{raw: "", state: "unknown", label: "unknown"},
		{raw: "restarting:starting", state: "restarting", health: "starting", running: true, label: "restarting:starting"},
	}
	for _, tc := range tests {
		st := ParseStatus(tc.raw)
		if st.State != tc.state || st.Health != tc.health {
			t.Errorf("ParseStatus(%q) = {%q,%q}, want {%q,%q}", tc.raw, st.State, st.Health, tc.state, tc.health)
		}
		if st.Running() != tc.running {
			t.Errorf("ParseStatus(%q).Running() = %v, want %v", tc.raw, st.Running(), tc.running)
		}
		if st.Degraded() != tc.degraded {
			t.Errorf("ParseStatus(%q).Degraded() = %v, want %v", tc.raw, st.Degraded(), tc.degraded)
		}
		if st.Label() != tc.label {
			t.Errorf("ParseStatus(%q).Label() = %q, want %q", tc.raw, st.Label(), tc.label)
		}
	}
}

func TestServerHealth(t *testing.T) {
	tests := []struct {
		name string
		srv  Server
		want ServerHealth
	}{
		{"healthy", Server{Settings: ServerSettings{IsReachable: true, IsUsable: true}}, ServerHealthy},
		{"unreachable", Server{Settings: ServerSettings{IsReachable: false, IsUsable: true}}, ServerUnreachable},
		{"unusable", Server{Settings: ServerSettings{IsReachable: true, IsUsable: false}}, ServerUnusable},
		{"disabled wins", Server{Settings: ServerSettings{ForceDisabled: true, IsReachable: true, IsUsable: true}}, ServerDisabled},
	}
	for _, tc := range tests {
		if got := tc.srv.Health(); got != tc.want {
			t.Errorf("%s: Health() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestApplicationDomains(t *testing.T) {
	app := Application{FQDN: "https://a.example.com, https://b.example.com ,"}
	got := app.Domains()
	if len(got) != 2 || got[0] != "https://a.example.com" || got[1] != "https://b.example.com" {
		t.Errorf("Domains() = %#v", got)
	}
	if app.PrimaryDomain() != "https://a.example.com" {
		t.Errorf("PrimaryDomain() = %q", app.PrimaryDomain())
	}
	if empty := (Application{}).Domains(); empty != nil {
		t.Errorf("Domains() on empty fqdn = %#v, want nil", empty)
	}
}

func TestDeploymentHelpers(t *testing.T) {
	created := time.Now().Add(-90 * time.Second)
	d := Deployment{
		Status:    "finished",
		Commit:    "abcdef1234567890",
		CreatedAt: created,
		UpdatedAt: created.Add(75 * time.Second),
	}
	if d.ShortCommit() != "abcdef1" {
		t.Errorf("ShortCommit() = %q", d.ShortCommit())
	}
	if d.InProgress() {
		t.Error("InProgress() = true for finished deployment")
	}
	if got := d.Duration(); got != 75*time.Second {
		t.Errorf("Duration() = %v, want 75s", got)
	}

	running := Deployment{Status: "in_progress", CreatedAt: created}
	if !running.InProgress() {
		t.Error("InProgress() = false for in_progress deployment")
	}
	if got := running.Duration(); got < 89*time.Second {
		t.Errorf("Duration() = %v, want ~90s measured to now", got)
	}
}

func TestParseDeploymentLogs(t *testing.T) {
	raw := `[{"output":"visible","type":"stdout","hidden":false},` +
		`{"output":"secret","type":"stdout","hidden":true},` +
		`{"output":"oops","type":"stderr","hidden":false}]`
	lines := ParseDeploymentLogs(raw)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (hidden entries dropped)", len(lines))
	}
	if lines[0].Output != "visible" {
		t.Errorf("lines[0].Output = %q", lines[0].Output)
	}
	if !lines[1].Stderr() {
		t.Error("lines[1].Stderr() = false, want true")
	}

	plain := ParseDeploymentLogs("line one\nline two")
	if len(plain) != 2 || plain[1].Output != "line two" {
		t.Errorf("plain text fallback = %#v", plain)
	}
	if ParseDeploymentLogs("   ") != nil {
		t.Error("blank input should parse to nil")
	}
}

func TestFetchInventoryJoinsAppsToServers(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/servers":
			_, _ = w.Write([]byte(`[
				{"uuid":"srv-b","name":"beta","ip":"10.0.0.2","settings":{"is_reachable":true,"is_usable":true}},
				{"uuid":"srv-a","name":"alpha","ip":"10.0.0.1","settings":{"is_reachable":true,"is_usable":true}}
			]`))
		case "/api/v1/servers/srv-a/resources":
			_, _ = w.Write([]byte(`[{"uuid":"app-1","name":"web","type":"application","status":"running:healthy"}]`))
		case "/api/v1/servers/srv-b/resources":
			_, _ = w.Write([]byte(`[{"uuid":"app-2","name":"api","type":"application","status":"exited:unhealthy"}]`))
		case "/api/v1/applications":
			_, _ = w.Write([]byte(`[
				{"uuid":"app-1","name":"web","status":"running:healthy"},
				{"uuid":"app-2","name":"api","status":"exited:unhealthy"},
				{"uuid":"app-3","name":"orphan","status":"running:unhealthy"}
			]`))
		case "/api/v1/deployments":
			_, _ = w.Write([]byte(`[{"deployment_uuid":"d1","application_name":"web","status":"in_progress"}]`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	inv, err := c.FetchInventory(context.Background())
	if err != nil {
		t.Fatalf("FetchInventory: %v", err)
	}

	// Servers sorted by name.
	if len(inv.Servers) != 2 || inv.Servers[0].Name != "alpha" {
		t.Errorf("servers not sorted by name: %+v", inv.Servers)
	}

	byUUID := map[string]Application{}
	for _, app := range inv.Apps {
		byUUID[app.UUID] = app
	}
	if got := byUUID["app-1"].ServerName; got != "alpha" {
		t.Errorf("app-1 server = %q, want alpha", got)
	}
	if got := byUUID["app-2"].ServerUUID; got != "srv-b" {
		t.Errorf("app-2 server uuid = %q, want srv-b", got)
	}
	// An application on no known server must still be listed, not dropped.
	if got := byUUID["app-3"].ServerName; got != "unknown" {
		t.Errorf("app-3 server = %q, want unknown", got)
	}

	running, degraded, stopped := inv.Counts()
	if running != 1 || degraded != 1 || stopped != 1 {
		t.Errorf("Counts() = (%d,%d,%d), want (1,1,1)", running, degraded, stopped)
	}

	if _, ok := inv.DeploymentForApp("web"); !ok {
		t.Error("DeploymentForApp(web) = false, want the in-flight deployment")
	}
	if _, ok := inv.DeploymentForApp("api"); ok {
		t.Error("DeploymentForApp(api) = true, want false")
	}
	if len(inv.RunningDeployments()) != 1 {
		t.Errorf("RunningDeployments() = %d, want 1", len(inv.RunningDeployments()))
	}
	if len(inv.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", inv.Warnings)
	}
}

func TestFetchInventoryWarnsOnUnreachableServer(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/servers":
			_, _ = w.Write([]byte(`[{"uuid":"srv-a","name":"alpha","settings":{"is_reachable":false}}]`))
		case "/api/v1/servers/srv-a/resources":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"server unreachable"}`))
		case "/api/v1/applications":
			_, _ = w.Write([]byte(`[{"uuid":"app-1","name":"web","status":"running:healthy"}]`))
		case "/api/v1/deployments":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
		}
	})

	inv, err := c.FetchInventory(context.Background())
	if err != nil {
		t.Fatalf("FetchInventory should tolerate per-server failures, got: %v", err)
	}
	if len(inv.Apps) != 1 {
		t.Errorf("apps = %d, want 1", len(inv.Apps))
	}
	if len(inv.Warnings) != 2 {
		t.Errorf("warnings = %v, want one per failed server plus the queue", inv.Warnings)
	}
}

func TestFetchInventoryFailsWhenServersFail(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/servers" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthenticated."}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := c.FetchInventory(context.Background()); err == nil {
		t.Fatal("want error when the servers call fails, got nil")
	}
}
