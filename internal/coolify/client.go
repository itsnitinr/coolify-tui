// Package coolify is a typed client for the Coolify v1 HTTP API.
//
// Authentication uses a bearer token of the form "{id}|{secret}" issued from
// Coolify's Security -> API Tokens screen. Endpoints used here need at least
// the read permission; deploy needs deploy, lifecycle actions need write, and
// environment variables and logs need read:sensitive.
package coolify

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// APIPrefix is appended to the instance URL to reach the versioned API.
	APIPrefix = "/api/v1"

	defaultTimeout = 20 * time.Second
	maxErrorBody   = 4 << 10
)

// Client talks to a single Coolify instance.
type Client struct {
	baseURL   *url.URL
	token     string
	http      *http.Client
	userAgent string
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying HTTP client. Mainly for tests.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithTimeout sets the per-request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.http.Timeout = d }
}

// WithInsecureSkipVerify disables TLS certificate verification, for instances
// behind a self-signed certificate.
func WithInsecureSkipVerify(skip bool) Option {
	return func(c *Client) {
		if !skip {
			return
		}
		c.http.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // opt-in
		}
	}
}

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// New builds a client for the given instance URL and API token. The URL may be
// given as a bare host ("coolify.example.com"), with a scheme, and with or
// without the /api/v1 suffix.
func New(instanceURL, token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("coolify: API token is required")
	}
	base, err := NormalizeURL(instanceURL)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("coolify: invalid instance URL: %w", err)
	}
	c := &Client{
		baseURL:   parsed,
		token:     strings.TrimSpace(token),
		http:      &http.Client{Timeout: defaultTimeout},
		userAgent: "coolify-tui",
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// NormalizeURL turns user input into a canonical API base URL ending in
// /api/v1. It defaults to https when no scheme is given.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("coolify: instance URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("coolify: invalid instance URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("coolify: unsupported URL scheme %q (want http or https)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("coolify: instance URL %q has no host", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	// Tolerate users pasting either the dashboard root or the API base.
	u.Path = strings.TrimSuffix(u.Path, "/api/v1")
	u.Path = strings.TrimSuffix(u.Path, "/api")
	u.Path += APIPrefix
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// BaseURL returns the API base URL, e.g. https://coolify.example.com/api/v1.
func (c *Client) BaseURL() string { return c.baseURL.String() }

// DashboardURL returns the instance root, without the API prefix, suitable for
// opening in a browser.
func (c *Client) DashboardURL() string {
	u := *c.baseURL
	u.Path = strings.TrimSuffix(u.Path, APIPrefix)
	return u.String()
}

// APIError is a non-2xx response from Coolify.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Sprintf("unauthorized (401): %s — check the API token", msg)
	case http.StatusForbidden:
		return fmt.Sprintf("forbidden (403): %s — the token is missing a required permission", msg)
	case http.StatusNotFound:
		return fmt.Sprintf("not found (404): %s", msg)
	case http.StatusTooManyRequests:
		return fmt.Sprintf("rate limited (429): %s — Coolify allows 200 requests/minute by default", msg)
	}
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.StatusCode, msg)
}

// Unauthorized reports whether the error is an auth failure (401 or 403).
func (e *APIError) Unauthorized() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

// IsUnauthorized reports whether err is an APIError with a 401 or 403 status.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Unauthorized()
}

// IsNotFound reports whether err is an APIError with a 404 status.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// StatusCode returns the HTTP status carried by err, or 0 if err is not an
// APIError.
func StatusCode(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// do performs a request and decodes a JSON response body into out. Pass a nil
// out to discard the body.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, out any) error {
	endpoint := c.baseURL.String() + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return fmt.Errorf("coolify: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		// Keep the token out of the surfaced error; url.Error includes the URL
		// but never headers, so this is only about wording.
		return fmt.Errorf("coolify: %s %s: %w", method, path, unwrapURLError(err))
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.apiError(resp, method, path)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("coolify: decode %s %s response: %w", method, path, err)
	}
	return nil
}

// doRaw performs a request and returns the undecoded JSON body, for endpoints
// whose response shape varies and needs inspecting before decoding.
func (c *Client) doRaw(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	endpoint := c.baseURL.String() + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("coolify: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coolify: %s %s: %w", method, path, unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.apiError(resp, method, path)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("coolify: read %s %s response: %w", method, path, err)
	}
	return body, nil
}

// doText performs a request and returns the raw body as a string, for the
// endpoints that answer text/plain rather than JSON.
func (c *Client) doText(ctx context.Context, method, path string) (string, error) {
	endpoint := c.baseURL.String() + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("coolify: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("coolify: %s %s: %w", method, path, unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", c.apiError(resp, method, path)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("coolify: read %s %s response: %w", method, path, err)
	}
	return strings.TrimSpace(string(body)), nil
}

func (c *Client) apiError(resp *http.Response, method, path string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	apiErr := &APIError{StatusCode: resp.StatusCode, Method: method, Path: path}

	// Coolify returns {"message": "..."} for most errors, and Laravel adds
	// {"errors": {"field": ["..."]}} for validation failures.
	var payload struct {
		Message string              `json:"message"`
		Error   string              `json:"error"`
		Errors  map[string][]string `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		switch {
		case payload.Message != "":
			apiErr.Message = payload.Message
		case payload.Error != "":
			apiErr.Message = payload.Error
		}
		if len(payload.Errors) > 0 {
			var parts []string
			for field, msgs := range payload.Errors {
				parts = append(parts, field+": "+strings.Join(msgs, ", "))
			}
			detail := strings.Join(parts, "; ")
			if apiErr.Message == "" {
				apiErr.Message = detail
			} else {
				apiErr.Message += " (" + detail + ")"
			}
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = strings.TrimSpace(string(body))
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			apiErr.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	return apiErr
}

// unwrapURLError strips the *url.Error wrapper so messages stay readable.
func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// --- Instance metadata ---

// Version returns the Coolify version string, e.g. "4.0.0-beta.397". It is the
// cheapest authenticated call, so it doubles as a credential check.
func (c *Client) Version(ctx context.Context) (string, error) {
	return c.doText(ctx, http.MethodGet, "/version")
}

// Teams lists the teams reachable with this token.
func (c *Client) Teams(ctx context.Context) ([]Team, error) {
	var teams []Team
	if err := c.do(ctx, http.MethodGet, "/teams", nil, &teams); err != nil {
		return nil, err
	}
	return teams, nil
}

// Projects lists the instance's projects.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var projects []Project
	if err := c.do(ctx, http.MethodGet, "/projects", nil, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// --- Servers ---

// Servers lists the servers managed by the instance.
func (c *Client) Servers(ctx context.Context) ([]Server, error) {
	var servers []Server
	if err := c.do(ctx, http.MethodGet, "/servers", nil, &servers); err != nil {
		return nil, err
	}
	return servers, nil
}

// Server fetches one server by UUID.
func (c *Client) Server(ctx context.Context, uuid string) (Server, error) {
	var server Server
	err := c.do(ctx, http.MethodGet, "/servers/"+url.PathEscape(uuid), nil, &server)
	return server, err
}

// ServerResources lists every resource deployed to a server, of any type.
func (c *Client) ServerResources(ctx context.Context, uuid string) ([]Resource, error) {
	var resources []Resource
	path := "/servers/" + url.PathEscape(uuid) + "/resources"
	if err := c.do(ctx, http.MethodGet, path, nil, &resources); err != nil {
		return nil, err
	}
	return resources, nil
}

// ServerDomains lists the domains served by a server.
func (c *Client) ServerDomains(ctx context.Context, uuid string) ([]string, error) {
	var payload []struct {
		IP      string   `json:"ip"`
		Domains []string `json:"domains"`
	}
	path := "/servers/" + url.PathEscape(uuid) + "/domains"
	if err := c.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return nil, err
	}
	var domains []string
	for _, entry := range payload {
		domains = append(domains, entry.Domains...)
	}
	return domains, nil
}

// --- Applications ---

// Applications lists every application visible to the token.
func (c *Client) Applications(ctx context.Context) ([]Application, error) {
	var apps []Application
	if err := c.do(ctx, http.MethodGet, "/applications", nil, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

// Application fetches one application by UUID.
func (c *Client) Application(ctx context.Context, uuid string) (Application, error) {
	var app Application
	err := c.do(ctx, http.MethodGet, "/applications/"+url.PathEscape(uuid), nil, &app)
	return app, err
}

// ApplicationEnvs lists an application's environment variables. Values require
// the read:sensitive token permission; without it Coolify returns 403.
func (c *Client) ApplicationEnvs(ctx context.Context, uuid string) ([]EnvVar, error) {
	var envs []EnvVar
	path := "/applications/" + url.PathEscape(uuid) + "/envs"
	if err := c.do(ctx, http.MethodGet, path, nil, &envs); err != nil {
		return nil, err
	}
	return envs, nil
}

// ApplicationLogs returns the tail of an application's container logs.
func (c *Client) ApplicationLogs(ctx context.Context, uuid string, lines int, timestamps bool) (string, error) {
	query := url.Values{}
	if lines > 0 {
		query.Set("lines", strconv.Itoa(lines))
	}
	if timestamps {
		query.Set("show_timestamps", "true")
	}
	var payload struct {
		Logs string `json:"logs"`
	}
	path := "/applications/" + url.PathEscape(uuid) + "/logs"
	if err := c.do(ctx, http.MethodGet, path, query, &payload); err != nil {
		return "", err
	}
	return payload.Logs, nil
}

// --- Lifecycle actions ---

// StartApplication starts a stopped application. Coolify implements start as a
// deployment, so the result may carry a deployment UUID.
func (c *Client) StartApplication(ctx context.Context, uuid string, force, instant bool) (ActionResult, error) {
	query := url.Values{}
	if force {
		query.Set("force", "true")
	}
	if instant {
		query.Set("instant_deploy", "true")
	}
	var result ActionResult
	path := "/applications/" + url.PathEscape(uuid) + "/start"
	err := c.do(ctx, http.MethodPost, path, query, &result)
	return result, err
}

// StopApplication stops a running application.
func (c *Client) StopApplication(ctx context.Context, uuid string) (ActionResult, error) {
	var result ActionResult
	path := "/applications/" + url.PathEscape(uuid) + "/stop"
	err := c.do(ctx, http.MethodPost, path, nil, &result)
	return result, err
}

// RestartApplication restarts a running application.
func (c *Client) RestartApplication(ctx context.Context, uuid string) (ActionResult, error) {
	var result ActionResult
	path := "/applications/" + url.PathEscape(uuid) + "/restart"
	err := c.do(ctx, http.MethodPost, path, nil, &result)
	return result, err
}

// --- Deployments ---

// Deploy queues a deployment for one or more resource UUIDs. Set force to
// rebuild without the Docker layer cache.
func (c *Client) Deploy(ctx context.Context, uuids []string, force bool) ([]ActionResult, error) {
	if len(uuids) == 0 {
		return nil, errors.New("coolify: deploy needs at least one resource UUID")
	}
	query := url.Values{}
	query.Set("uuid", strings.Join(uuids, ","))
	if force {
		query.Set("force", "true")
	}
	var payload DeployResponse
	if err := c.do(ctx, http.MethodPost, "/deploy", query, &payload); err != nil {
		return nil, err
	}
	return payload.Deployments, nil
}

// Deployments lists currently queued and running deployments across the
// instance.
//
// The handler sorts with a Laravel Collection, which preserves array keys, so
// this can come back as a key-indexed object instead of an array once the sort
// reorders anything. decodeCollection normalises both.
func (c *Client) Deployments(ctx context.Context) ([]Deployment, error) {
	body, err := c.doRaw(ctx, http.MethodGet, "/deployments", nil)
	if err != nil {
		return nil, err
	}
	return decodeCollection[Deployment](body, "deployments")
}

// ApplicationDeployments lists an application's deployment history, newest
// first. take caps the number of records; pass 0 for the API default.
//
// This endpoint returns a paginated envelope, {"count": N, "deployments": [...]},
// even though Coolify's OpenAPI spec documents a bare array.
func (c *Client) ApplicationDeployments(ctx context.Context, uuid string, skip, take int) ([]Deployment, error) {
	query := url.Values{}
	if skip > 0 {
		query.Set("skip", strconv.Itoa(skip))
	}
	if take > 0 {
		query.Set("take", strconv.Itoa(take))
	}
	path := "/deployments/applications/" + url.PathEscape(uuid)
	body, err := c.doRaw(ctx, http.MethodGet, path, query)
	if err != nil {
		return nil, err
	}
	return decodeCollection[Deployment](body, "deployments")
}

// Deployment fetches a single deployment, including its build logs.
func (c *Client) Deployment(ctx context.Context, uuid string) (Deployment, error) {
	var deployment Deployment
	err := c.do(ctx, http.MethodGet, "/deployments/"+url.PathEscape(uuid), nil, &deployment)
	return deployment, err
}

// CancelDeployment cancels an in-flight deployment.
func (c *Client) CancelDeployment(ctx context.Context, uuid string) (ActionResult, error) {
	var result ActionResult
	path := "/deployments/" + url.PathEscape(uuid) + "/cancel"
	err := c.do(ctx, http.MethodPost, path, nil, &result)
	return result, err
}
