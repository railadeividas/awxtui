// Package awx is a minimal client for the AWX / Ansible Automation Platform v2 API.
package awx

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a single AWX instance with a bearer token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New builds a client for baseURL, e.g. https://awx.example.com. Trailing
// slashes and an explicit /api/v2 suffix are both tolerated.
func New(baseURL, token string, insecure bool) *Client {
	base := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/api/v2")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		baseURL: base,
		token:   strings.TrimSpace(token),
		http:    &http.Client{Timeout: 30 * time.Second, Transport: tr},
	}
}

func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.baseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("%s %s: %s: %s", method, path, res.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	if s, ok := out.(*string); ok {
		raw, err := io.ReadAll(res.Body)
		*s = string(raw)
		return err
	}
	return json.NewDecoder(res.Body).Decode(out)
}

type page[T any] struct {
	Count   int `json:"count"`
	Results []T `json:"results"`
}

func list[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var p page[T]
	if err := c.do(ctx, http.MethodGet, path, nil, &p); err != nil {
		return nil, err
	}
	return p.Results, nil
}

// Me returns the username of the authenticated user, used as a connection check.
func (c *Client) Me(ctx context.Context) (string, error) {
	users, err := list[struct {
		Username string `json:"username"`
	}](ctx, c, "/api/v2/me/")
	if err != nil {
		return "", err
	}
	if len(users) == 0 {
		return "", fmt.Errorf("token accepted but no user returned")
	}
	return users[0].Username, nil
}

// JobTemplate is a launchable template.
type JobTemplate struct {
	ID                   int    `json:"id"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	JobType              string `json:"job_type"`
	Playbook             string `json:"playbook"`
	AskVariablesOnLaunch bool   `json:"ask_variables_on_launch"`
	SummaryFields        struct {
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
		Inventory struct {
			Name string `json:"name"`
		} `json:"inventory"`
		LastJob struct {
			ID     int    `json:"id"`
			Status string `json:"status"`
		} `json:"last_job"`
	} `json:"summary_fields"`
	LastJobRun *time.Time `json:"last_job_run"`
}

func (c *Client) JobTemplates(ctx context.Context) ([]JobTemplate, error) {
	return list[JobTemplate](ctx, c, "/api/v2/job_templates/?order_by=name&page_size=200")
}

// Job is a single (running or finished) job run.
type Job struct {
	ID            int        `json:"id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	Failed        bool       `json:"failed"`
	Started       *time.Time `json:"started"`
	Finished      *time.Time `json:"finished"`
	Elapsed       float64    `json:"elapsed"`
	JobType       string     `json:"job_type"`
	SummaryFields struct {
		CreatedBy struct {
			Username string `json:"username"`
		} `json:"created_by"`
		Inventory struct {
			Name string `json:"name"`
		} `json:"inventory"`
	} `json:"summary_fields"`
}

// IsRunning reports whether the job may still produce output.
func (j Job) IsRunning() bool {
	switch j.Status {
	case "new", "pending", "waiting", "running":
		return true
	}
	return false
}

func (c *Client) Jobs(ctx context.Context) ([]Job, error) {
	return list[Job](ctx, c, "/api/v2/jobs/?order_by=-id&page_size=100")
}

func (c *Client) Job(ctx context.Context, id int) (Job, error) {
	var j Job
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v2/jobs/%d/", id), nil, &j)
	return j, err
}

// Inventory holds hosts to run against.
type Inventory struct {
	ID                      int    `json:"id"`
	Name                    string `json:"name"`
	Description             string `json:"description"`
	TotalHosts              int    `json:"total_hosts"`
	TotalGroups             int    `json:"total_groups"`
	HostsWithActiveFailures int    `json:"hosts_with_active_failures"`
	SummaryFields           struct {
		Organization struct {
			Name string `json:"name"`
		} `json:"organization"`
	} `json:"summary_fields"`
}

func (c *Client) Inventories(ctx context.Context) ([]Inventory, error) {
	return list[Inventory](ctx, c, "/api/v2/inventories/?order_by=name&page_size=200")
}

// Project is a source of playbooks.
type Project struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	SCMType     string     `json:"scm_type"`
	SCMURL      string     `json:"scm_url"`
	SCMBranch   string     `json:"scm_branch"`
	Status      string     `json:"status"`
	LastUpdated *time.Time `json:"last_updated"`
}

func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	return list[Project](ctx, c, "/api/v2/projects/?order_by=name&page_size=200")
}

// Host belongs to an inventory.
type Host struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Enabled           bool   `json:"enabled"`
	HasActiveFailures bool   `json:"has_active_failures"`
}

func (c *Client) Hosts(ctx context.Context, inventoryID int) ([]Host, error) {
	return list[Host](ctx, c, fmt.Sprintf("/api/v2/inventories/%d/hosts/?order_by=name&page_size=200", inventoryID))
}

// Launch starts a job template and returns the created job. extraVars, when
// non-empty, must be a JSON object and is sent as extra_vars.
func (c *Client) Launch(ctx context.Context, templateID int, extraVars string) (Job, error) {
	payload := map[string]any{}
	if v := strings.TrimSpace(extraVars); v != "" {
		var vars map[string]any
		if err := json.Unmarshal([]byte(v), &vars); err != nil {
			return Job{}, fmt.Errorf("extra vars must be a JSON object: %w", err)
		}
		payload["extra_vars"] = vars
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Job{}, err
	}
	var j Job
	err = c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v2/job_templates/%d/launch/", templateID), strings.NewReader(string(raw)), &j)
	return j, err
}

// Cancel requests cancellation of a running job.
func (c *Client) Cancel(ctx context.Context, jobID int) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v2/jobs/%d/cancel/", jobID), strings.NewReader("{}"), nil)
}

// Stdout returns the job output as ANSI-coloured text.
func (c *Client) Stdout(ctx context.Context, jobID int) (string, error) {
	var out string
	q := url.Values{"format": {"ansi"}}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v2/jobs/%d/stdout/?%s", jobID, q.Encode()), nil, &out)
	return out, err
}
