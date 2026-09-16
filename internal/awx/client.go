// Package awx is a minimal client for the AWX / Ansible Automation Platform v2 API.
package awx

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a single AWX instance with a bearer token.
type Client struct {
	baseURL  string
	token    string
	http     *http.Client
	readOnly bool
}

// ErrReadOnly is returned when a read-only client is asked to change state.
var ErrReadOnly = errors.New("client is read-only: refusing to modify AWX")

// ReadOnly returns the client restricted to GET requests, so it cannot launch,
// cancel or modify anything. Use it when pointing at a production instance.
func (c *Client) ReadOnly() *Client {
	c.readOnly = true
	return c
}

// IsReadOnly reports whether state-changing requests are blocked.
func (c *Client) IsReadOnly() bool { return c.readOnly }

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

func (c *Client) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if c.readOnly && method != http.MethodGet {
		return nil, fmt.Errorf("%w (%s %s)", ErrReadOnly, method, path)
	}
	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.baseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
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

// Page is one page of an AWX list endpoint. Next is the API-supplied path of
// the following page, empty when this is the last one.
type Page[T any] struct {
	Count   int    `json:"count"`
	Next    string `json:"next"`
	Results []T    `json:"results"`
}

// PageSize is how many records each list request asks for. AWX caps this at
// its own max_page_size (200 by default).
const PageSize = 200

func listPage[T any](ctx context.Context, c *Client, path string) (Page[T], error) {
	var p Page[T]
	err := c.do(ctx, http.MethodGet, path, nil, &p)
	return p, err
}

// list fetches a single page and discards the paging metadata. Only use it for
// endpoints that cannot meaningfully have a second page.
func list[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	p, err := listPage[T](ctx, c, path)
	return p.Results, err
}

// firstOr returns pageURL, or first when no page was given. AWX hands back
// Next as a root-relative path, which do() resolves against the base URL.
func firstOr(pageURL, first string) string {
	if strings.TrimSpace(pageURL) == "" {
		return first
	}
	return pageURL
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

// JobTemplates returns a page of job templates. Pass the Next value of a
// previous page to continue; an empty pageURL starts at the beginning.
func (c *Client) JobTemplates(ctx context.Context, pageURL string) (Page[JobTemplate], error) {
	return listPage[JobTemplate](ctx, c,
		firstOr(pageURL, fmt.Sprintf("/api/v2/job_templates/?order_by=name&page_size=%d", PageSize)))
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

// Jobs returns a page of jobs, newest first.
func (c *Client) Jobs(ctx context.Context, pageURL string) (Page[Job], error) {
	return listPage[Job](ctx, c,
		firstOr(pageURL, fmt.Sprintf("/api/v2/jobs/?order_by=-id&page_size=%d", jobsPageSize)))
}

// jobsPageSize is smaller than PageSize: job lists are effectively unbounded,
// so they are paged in on demand rather than read whole.
const jobsPageSize = 100

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

// Inventories returns a page of inventories.
func (c *Client) Inventories(ctx context.Context, pageURL string) (Page[Inventory], error) {
	return listPage[Inventory](ctx, c,
		firstOr(pageURL, fmt.Sprintf("/api/v2/inventories/?order_by=name&page_size=%d", PageSize)))
}

// AllInventories walks every page of inventories, up to maxPages, for callers
// that need a complete list rather than a screenful.
func (c *Client) AllInventories(ctx context.Context, maxPages int) ([]Inventory, error) {
	var out []Inventory
	next := ""
	for i := 0; i < maxPages; i++ {
		p, err := c.Inventories(ctx, next)
		if err != nil {
			return out, err
		}
		out = append(out, p.Results...)
		if p.Next == "" {
			break
		}
		next = p.Next
	}
	return out, nil
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

// Projects returns a page of projects.
func (c *Client) Projects(ctx context.Context, pageURL string) (Page[Project], error) {
	return listPage[Project](ctx, c,
		firstOr(pageURL, fmt.Sprintf("/api/v2/projects/?order_by=name&page_size=%d", PageSize)))
}

// Host belongs to an inventory.
type Host struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Enabled           bool   `json:"enabled"`
	HasActiveFailures bool   `json:"has_active_failures"`
}

// Hosts returns a page of an inventory's hosts.
func (c *Client) Hosts(ctx context.Context, inventoryID int, pageURL string) (Page[Host], error) {
	return listPage[Host](ctx, c, firstOr(pageURL,
		fmt.Sprintf("/api/v2/inventories/%d/hosts/?order_by=name&page_size=%d", inventoryID, PageSize)))
}

// Cancel requests cancellation of a running job.
func (c *Client) Cancel(ctx context.Context, jobID int) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v2/jobs/%d/cancel/", jobID), strings.NewReader("{}"), nil)
}

// ErrStdoutNotReady is returned when AWX accepts the stdout request but has
// not finished collating the job's output yet (HTTP 202). This is normal for
// jobs that are still running; read JobEvents instead.
var ErrStdoutNotReady = errors.New("job stdout is not ready yet")

// Stdout returns the whole job output as ANSI-coloured text. AWX only serves
// this reliably once a job has finished and its events are processed.
func (c *Client) Stdout(ctx context.Context, jobID int) (string, error) {
	q := url.Values{"format": {"ansi"}}
	path := fmt.Sprintf("/api/v2/jobs/%d/stdout/?%s", jobID, q.Encode())
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain")
	res, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusAccepted {
		return "", ErrStdoutNotReady
	}
	if res.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return "", fmt.Errorf("GET %s: %s: %s", path, res.Status, strings.TrimSpace(string(msg)))
	}
	raw, err := io.ReadAll(res.Body)
	return string(raw), err
}

// EventPageSize is how many job events are fetched per request.
const EventPageSize = 200

// JobEvent is one entry of a job's output stream. This is the same data the
// AWX web UI renders, and unlike /stdout/ it is available while a job runs.
type JobEvent struct {
	Counter   int    `json:"counter"`
	Stdout    string `json:"stdout"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// JobEvents returns up to EventPageSize events with a counter above after,
// oldest first, along with the highest counter seen.
func (c *Client) JobEvents(ctx context.Context, jobID, after int) ([]JobEvent, error) {
	path := fmt.Sprintf("/api/v2/jobs/%d/job_events/?order_by=counter&page_size=%d&counter__gt=%d",
		jobID, EventPageSize, after)
	return list[JobEvent](ctx, c, path)
}
