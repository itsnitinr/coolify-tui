package coolify

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Inventory is a point-in-time snapshot of everything the TUI displays.
type Inventory struct {
	Servers     []Server
	Apps        []Application
	Deployments []Deployment
	FetchedAt   time.Time

	// Warnings collects non-fatal problems, such as a single unreachable
	// server whose resource listing failed while the rest succeeded.
	Warnings []string
}

// AppsByServer groups applications under their server UUID.
func (inv Inventory) AppsByServer() map[string][]Application {
	out := make(map[string][]Application, len(inv.Servers))
	for _, app := range inv.Apps {
		out[app.ServerUUID] = append(out[app.ServerUUID], app)
	}
	return out
}

// RunningDeployments returns only the deployments still in flight.
func (inv Inventory) RunningDeployments() []Deployment {
	var out []Deployment
	for _, d := range inv.Deployments {
		if d.InProgress() {
			out = append(out, d)
		}
	}
	return out
}

// DeploymentForApp returns the in-flight deployment for an application name, if
// any. Coolify's queue payload identifies applications by name rather than
// UUID, so that is what we match on.
func (inv Inventory) DeploymentForApp(appName string) (Deployment, bool) {
	for _, d := range inv.Deployments {
		if d.InProgress() && d.ApplicationName == appName {
			return d, true
		}
	}
	return Deployment{}, false
}

// Counts summarises application health for the status bar.
func (inv Inventory) Counts() (running, degraded, stopped int) {
	for _, app := range inv.Apps {
		st := ParseStatus(app.Status)
		switch {
		case st.Degraded():
			degraded++
		case st.Running():
			running++
		default:
			stopped++
		}
	}
	return running, degraded, stopped
}

// FetchInventory loads servers, applications and the deployment queue, then
// joins applications to the server they run on.
//
// The join is needed because GET /applications does not report a server: it
// only carries a destination_id. GET /servers/{uuid}/resources does report
// which resources live on a server, so we walk the servers and index by UUID.
func (c *Client) FetchInventory(ctx context.Context) (Inventory, error) {
	var (
		servers     []Server
		apps        []Application
		deployments []Deployment

		serverErr, appErr error
		wg                sync.WaitGroup
		mu                sync.Mutex
		warnings          []string
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		servers, serverErr = c.Servers(ctx)
	}()
	go func() {
		defer wg.Done()
		apps, appErr = c.Applications(ctx)
	}()
	go func() {
		defer wg.Done()
		var err error
		// A failure here is not fatal: the queue is supplementary detail.
		if deployments, err = c.Deployments(ctx); err != nil {
			mu.Lock()
			warnings = append(warnings, "deployment queue unavailable: "+err.Error())
			mu.Unlock()
		}
	}()
	wg.Wait()

	if serverErr != nil {
		return Inventory{}, fmt.Errorf("list servers: %w", serverErr)
	}
	if appErr != nil {
		return Inventory{}, fmt.Errorf("list applications: %w", appErr)
	}

	// Map resource UUID -> server, fetching each server's resources in
	// parallel. Unreachable servers fail individually and only warn.
	type ownership struct{ name, uuid string }
	owner := make(map[string]ownership, len(apps))

	wg = sync.WaitGroup{}
	for _, srv := range servers {
		if srv.Settings.ForceDisabled {
			continue
		}
		wg.Add(1)
		go func(srv Server) {
			defer wg.Done()
			resources, err := c.ServerResources(ctx, srv.UUID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("server %s: %v", srv.Name, err))
				return
			}
			for _, res := range resources {
				owner[res.UUID] = ownership{name: srv.Name, uuid: srv.UUID}
			}
		}(srv)
	}
	wg.Wait()

	for i := range apps {
		if own, ok := owner[apps[i].UUID]; ok {
			apps[i].ServerName = own.name
			apps[i].ServerUUID = own.uuid
		} else {
			apps[i].ServerName = "unknown"
		}
	}

	sort.Slice(servers, func(i, j int) bool {
		return strings.ToLower(servers[i].Name) < strings.ToLower(servers[j].Name)
	})
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].ServerName != apps[j].ServerName {
			return strings.ToLower(apps[i].ServerName) < strings.ToLower(apps[j].ServerName)
		}
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
	sort.Slice(warnings, func(i, j int) bool { return warnings[i] < warnings[j] })

	return Inventory{
		Servers:     servers,
		Apps:        apps,
		Deployments: deployments,
		FetchedAt:   time.Now(),
		Warnings:    warnings,
	}, nil
}

// LogLine is one entry of a deployment's build log.
type LogLine struct {
	Output    string `json:"output"`
	Command   string `json:"command"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Hidden    bool   `json:"hidden"`
	Order     int    `json:"order"`
}

// Stderr reports whether the line was written to standard error.
func (l LogLine) Stderr() bool { return l.Type == "stderr" }

// ParseDeploymentLogs decodes the deployment log payload. Coolify stores it as
// a JSON-encoded array of entries, but older instances and cancelled builds can
// return plain text, so fall back to splitting lines.
func ParseDeploymentLogs(raw string) []LogLine {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var lines []LogLine
		if err := json.Unmarshal([]byte(raw), &lines); err == nil {
			out := make([]LogLine, 0, len(lines))
			for _, l := range lines {
				if l.Hidden {
					continue
				}
				out = append(out, l)
			}
			return out
		}
	}
	var out []LogLine
	for _, line := range strings.Split(raw, "\n") {
		out = append(out, LogLine{Output: line})
	}
	return out
}
