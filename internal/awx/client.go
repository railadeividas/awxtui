// Package awx is a minimal client for the AWX / Ansible Automation Platform v2 API.
package awx

import (
	"bytes"
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
	var payload []byte
	if body != nil {
		var err error
		if payload, err = io.ReadAll(body); err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	res, err := c.send(req, payload)
	if err != nil {
		return c.redactErr(err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return c.redactErr(fmt.Errorf("%s %s: %s: %s",
			method, path, res.Status, strings.TrimSpace(string(msg))))
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

const (
	// maxAttempts includes the first try.
	maxAttempts = 3
	baseBackoff = 250 * time.Millisecond
	maxBackoff  = 4 * time.Second
)

// send performs a request, retrying transient failures. GETs are safe to
// repeat, so they are retried on network errors and 5xx as well; anything that
// changes state is only retried on 429, which AWX returns before doing any
// work. payload is the body to replay on a retry, if any.
func (c *Client) send(req *http.Request, payload []byte) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		if payload != nil {
			req.Body = io.NopCloser(bytes.NewReader(payload))
		}
		res, err := c.http.Do(req)

		last := attempt >= maxAttempts
		if !shouldRetry(req.Method, res, err) || last || req.Context().Err() != nil {
			return res, err
		}
		wait := backoff(attempt, res)
		if res != nil {
			res.Body.Close()
		}
		select {
		case <-time.After(wait):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
}

// shouldRetry reports whether a failure is worth repeating. A POST that may
// already have launched a job is never repeated on an ambiguous failure.
func shouldRetry(method string, res *http.Response, err error) bool {
	idempotent := method == http.MethodGet || method == http.MethodHead
	if err != nil {
		return idempotent
	}
	if res == nil {
		return false
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return idempotent && res.StatusCode >= 500
}

// backoff grows the delay each attempt, honouring Retry-After when AWX sends
// one (it does when rate limiting).
func backoff(attempt int, res *http.Response) time.Duration {
	if res != nil {
		if v := res.Header.Get("Retry-After"); v != "" {
			if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs >= 0 {
				return min(time.Duration(secs)*time.Second, maxBackoff)
			}
			if when, err := http.ParseTime(strings.TrimSpace(v)); err == nil {
				if d := time.Until(when); d > 0 {
					return min(d, maxBackoff)
				}
			}
		}
	}
	d := baseBackoff << (attempt - 1)
	return min(d, maxBackoff)
}

// minRedactable is the shortest token worth substituting out of a message.
// Replacing a one- or two-character string would corrupt the text it appears
// in, and something that short is not a credential worth protecting anyway.
const minRedactable = 8

// redactErr keeps the token out of anything we might show or log, even though
// it travels in a header rather than the URL.
func (c *Client) redactErr(err error) error {
	if err == nil || len(c.token) < minRedactable {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, c.token) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, c.token, "<token redacted>"))
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
// Next as a root-relative path (query string included, so paging stays inside
// a search), which do() resolves against the base URL.
func firstOr(pageURL, first string) string {
	if strings.TrimSpace(pageURL) == "" {
		return first
	}
	return pageURL
}

// listURL builds a first-page URL. A non-empty search uses AWX's server-side
// search, which matches across the endpoint's searchable fields rather than
// only the names we happen to have loaded.
func listURL(path, orderBy string, pageSize int, search string) string {
	q := url.Values{
		"order_by":  {orderBy},
		"page_size": {strconv.Itoa(pageSize)},
	}
	if s := strings.TrimSpace(search); s != "" {
		q.Set("search", s)
	}
	return path + "?" + q.Encode()
}

// User identifies the account a token belongs to.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

// Me returns the authenticated user, used as a connection check and as the
// identity a "mine" filter is built from. The id matters as well as the name:
// /api/v2/unified_jobs/?created_by= takes the numeric id, and a username can
// be renamed out from under a saved filter.
func (c *Client) Me(ctx context.Context) (User, error) {
	users, err := list[User](ctx, c, "/api/v2/me/")
	if err != nil {
		return User{}, err
	}
	if len(users) == 0 {
		return User{}, fmt.Errorf("token accepted but no user returned")
	}
	return users[0], nil
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
func (c *Client) JobTemplates(ctx context.Context, pageURL, search string) (Page[JobTemplate], error) {
	return listPage[JobTemplate](ctx, c, firstOr(pageURL, listURL("/api/v2/job_templates/", "name", PageSize, search)))
}

// WorkflowJobTemplate is a launchable workflow: a graph of nodes, each
// running its own job template, project sync or inventory sync. Unlike a
// JobTemplate it has no playbook or credentials of its own — those belong to
// its nodes — so launching one only ever prompts for what the workflow itself
// asks: inventory, limit, SCM branch, tags, labels, survey answers.
type WorkflowJobTemplate struct {
	ID            int        `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	SurveyEnabled bool       `json:"survey_enabled"`
	Status        string     `json:"status"`
	LastJobRun    *time.Time `json:"last_job_run"`
	LastJobFailed bool       `json:"last_job_failed"`
	SummaryFields struct {
		Organization struct {
			Name string `json:"name"`
		} `json:"organization"`
		Inventory struct {
			Name string `json:"name"`
		} `json:"inventory"`
	} `json:"summary_fields"`
}

// LastRunStatus normalises Status for display: AWX says "never updated" for
// a workflow that has never run, where every other status badge in awxtui
// expects an empty string instead.
func (t WorkflowJobTemplate) LastRunStatus() string {
	if t.Status == "never updated" {
		return ""
	}
	return t.Status
}

// WorkflowJobTemplates returns a page of workflow job templates. Pass the
// Next value of a previous page to continue; an empty pageURL starts at the
// beginning.
func (c *Client) WorkflowJobTemplates(ctx context.Context, pageURL, search string) (Page[WorkflowJobTemplate], error) {
	return listPage[WorkflowJobTemplate](ctx, c, firstOr(pageURL, listURL("/api/v2/workflow_job_templates/", "name", PageSize, search)))
}

// WorkflowJobTemplatesByID reads a set of workflow job templates by id.
func (c *Client) WorkflowJobTemplatesByID(ctx context.Context, ids []int) ([]WorkflowJobTemplate, error) {
	return listByIDs[WorkflowJobTemplate](ctx, c, "/api/v2/workflow_job_templates/", ids)
}

// Schedule is a recurrence rule that launches a job template, project
// update, inventory sync or system job on its own. AWX auto-creates a few
// system-job schedules (cleanup, activity stream) alongside user ones.
type Schedule struct {
	ID            int        `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	RRule         string     `json:"rrule"`
	Enabled       bool       `json:"enabled"`
	NextRun       *time.Time `json:"next_run"`
	Timezone      string     `json:"timezone"`
	Created       *time.Time `json:"created"`
	Modified      *time.Time `json:"modified"`
	SummaryFields struct {
		UnifiedJobTemplate struct {
			ID             int    `json:"id"`
			Name           string `json:"name"`
			UnifiedJobType string `json:"unified_job_type"`
		} `json:"unified_job_template"`
		UserCapabilities struct {
			Edit bool `json:"edit"`
		} `json:"user_capabilities"`
	} `json:"summary_fields"`
}

// Schedules returns a page of every schedule across all templates and
// projects. Pass the Next value of a previous page to continue; an empty
// pageURL starts at the beginning.
func (c *Client) Schedules(ctx context.Context, pageURL, search string) (Page[Schedule], error) {
	return listPage[Schedule](ctx, c, firstOr(pageURL, listURL("/api/v2/schedules/", "name", PageSize, search)))
}

// SetScheduleEnabled flips a schedule's enabled flag without touching its
// recurrence rule or any other field.
func (c *Client) SetScheduleEnabled(ctx context.Context, id int, enabled bool) error {
	body, err := json.Marshal(map[string]bool{"enabled": enabled})
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPatch, fmt.Sprintf("/api/v2/schedules/%d/", id), bytes.NewReader(body), nil)
}

// Job is a single (running or finished) run of something: a playbook job, a
// project SCM update or an inventory sync. AWX serialises all three with the
// same core fields and tells them apart with Type.
type Job struct {
	ID            int        `json:"id"`
	Type          string     `json:"type"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	Failed        bool       `json:"failed"`
	Started       *time.Time `json:"started"`
	Finished      *time.Time `json:"finished"`
	Elapsed       float64    `json:"elapsed"`
	JobType       string     `json:"job_type"`
	LaunchType    string     `json:"launch_type"`
	ExtraVars     string     `json:"extra_vars"`
	Limit         string     `json:"limit"`
	JobTags       string     `json:"job_tags"`
	SkipTags      string     `json:"skip_tags"`
	SummaryFields struct {
		CreatedBy struct {
			ID       int    `json:"id"`
			Username string `json:"username"`
		} `json:"created_by"`
		Inventory struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"inventory"`
		// Project is set for a project update in place of Inventory, which a
		// project update's record has no use for.
		Project struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"project"`
		JobTemplate struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"job_template"`
		// WorkflowJobTemplate is set instead of JobTemplate when this record
		// is a workflow job. AWX leaves it null for the implicit workflow a
		// sliced job template creates, which is how one is told apart from a
		// run launched from a real workflow job template.
		WorkflowJobTemplate struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"workflow_job_template"`
		Credentials []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"credentials"`
		// Credential is the one AWX puts on a project update in place of the
		// Credentials list, which is always empty there.
		Credential struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"credential"`
		ExecutionEnvironment NamedRef `json:"execution_environment"`
	} `json:"summary_fields"`
}

// Resource is the API collection a run lives in. AWX keeps playbook jobs,
// project updates and inventory syncs in three separate collections, each with
// its own detail, stdout, events and cancel endpoints, and the /api/v2/jobs/
// list holds playbook jobs alone. Reading the output of a sync therefore means
// asking a different collection, not the same one with a different id.
type Resource string

const (
	ResourceJobs             Resource = "jobs"
	ResourceProjectUpdates   Resource = "project_updates"
	ResourceInventoryUpdates Resource = "inventory_updates"
	ResourceWorkflowJobs     Resource = "workflow_jobs"
)

// eventsPath is the events sub-resource of a collection. Playbook jobs call it
// job_events; both kinds of update call it events.
func (r Resource) eventsPath() string {
	if r == ResourceJobs {
		return "job_events"
	}
	return "events"
}

// recordType is the value AWX puts in the "type" field of a record from this
// collection. The two are not the same word: /api/v2/project_updates/ serves
// records of type "project_update".
func (r Resource) recordType() string {
	switch r {
	case ResourceProjectUpdates:
		return "project_update"
	case ResourceInventoryUpdates:
		return "inventory_update"
	case ResourceWorkflowJobs:
		return "workflow_job"
	default:
		return "job"
	}
}

// Resource reports which collection this record belongs to, from the type AWX
// puts in the record itself. An unknown or missing type falls back to jobs,
// which is what every record served from /api/v2/jobs/ is.
func (j Job) Resource() Resource {
	for _, r := range []Resource{ResourceProjectUpdates, ResourceInventoryUpdates, ResourceWorkflowJobs} {
		if j.Type == r.recordType() {
			return r
		}
	}
	return ResourceJobs
}

// IsSync reports whether this run is a project update or an inventory sync
// rather than a playbook job.
func (j Job) IsSync() bool {
	return j.Resource() != ResourceJobs && j.Resource() != ResourceWorkflowJobs
}

// IsWorkflow reports whether this run is a workflow job: a graph of nodes
// rather than a single playbook run. A workflow job has no stdout of its
// own — its output lives per-node, under /workflow_nodes/, which awxtui does
// not render yet — so it is shown as launch details rather than output.
func (j Job) IsWorkflow() bool { return j.Type == "workflow_job" }

// IsRunning reports whether the job may still produce output.
func (j Job) IsRunning() bool {
	switch j.Status {
	case "new", "pending", "waiting", "running":
		return true
	}
	return false
}

// Jobs returns a page of jobs, newest first.
func (c *Client) Jobs(ctx context.Context, pageURL, search string) (Page[Job], error) {
	return listPage[Job](ctx, c, firstOr(pageURL, listURL("/api/v2/jobs/", "-id", jobsPageSize, search)))
}

// jobsPageSize is smaller than PageSize: job lists are effectively unbounded,
// so they are paged in on demand rather than read whole.
const jobsPageSize = 100

// Supported reports whether awxtui can show this run's output.
// /api/v2/unified_jobs/ also serves ad hoc commands and system jobs, which
// live in collections awxtui does not implement, and workflow jobs, which
// have no stdout of their own — their output lives per-node. Resource would
// quietly call an ad hoc command or system job a job and then ask
// /api/v2/jobs/ for output that is not there.
func (j Job) Supported() bool {
	switch j.Type {
	case "", ResourceJobs.recordType(),
		ResourceProjectUpdates.recordType(), ResourceInventoryUpdates.recordType():
		return true
	}
	return false
}

// JobFilter narrows a unified job list, server-side. Every field AWX can
// answer for itself belongs here rather than in a loop over loaded rows: the
// list is paged, so filtering what happens to be on screen would hide
// everything that is not.
type JobFilter struct {
	// CreatedBy is a user id. It is what makes a personal history usable at
	// all: 310 runs out of 170290 on the instance this was built against.
	CreatedBy int
	// Status is an AWX job status: running, failed, successful…
	Status string
	// Type is an AWX record type: job, project_update, inventory_update.
	Type string
}

func (f JobFilter) query() string {
	var q string
	if f.CreatedBy > 0 {
		q += "&created_by=" + strconv.Itoa(f.CreatedBy)
	}
	if f.Status != "" {
		q += "&status=" + url.QueryEscape(f.Status)
	}
	if f.Type != "" {
		q += "&type=" + url.QueryEscape(f.Type)
	}
	return q
}

// UnifiedJobs returns a page of runs of every kind — playbook jobs, project
// updates and inventory syncs — newest first. /api/v2/jobs/ holds playbook
// jobs alone, so a sync started from awxtui never appears there; this is the
// only list that shows everything that ran. AWX composes ?search= with every
// filter above.
func (c *Client) UnifiedJobs(ctx context.Context, pageURL, search string, f JobFilter) (Page[Job], error) {
	first := listURL("/api/v2/unified_jobs/", "-id", jobsPageSize, search) + f.query()
	return listPage[Job](ctx, c, firstOr(pageURL, first))
}

// maxByID bounds one id__in lookup: AWX takes the ids in the query string,
// and a URL is not unbounded.
const maxByID = 100

// listByIDs reads a named set of records in one request. Records AWX no
// longer has are simply absent from the answer, which is how a deleted one is
// told apart from a failed lookup.
func listByIDs[T any](ctx context.Context, c *Client, path string, ids []int) ([]T, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > maxByID {
		ids = ids[:maxByID]
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	q := url.Values{
		"id__in":    {strings.Join(parts, ",")},
		"page_size": {strconv.Itoa(len(ids))},
	}
	return list[T](ctx, c, path+"?"+q.Encode())
}

// UnifiedJobsByID re-reads a set of runs of any kind in one request, so a
// locally kept list of pins can be refreshed without one GET per entry.
func (c *Client) UnifiedJobsByID(ctx context.Context, ids []int) ([]Job, error) {
	return listByIDs[Job](ctx, c, "/api/v2/unified_jobs/", ids)
}

// JobTemplatesByID reads a set of templates by id.
func (c *Client) JobTemplatesByID(ctx context.Context, ids []int) ([]JobTemplate, error) {
	return listByIDs[JobTemplate](ctx, c, "/api/v2/job_templates/", ids)
}

// InventoriesByID reads a set of inventories by id.
func (c *Client) InventoriesByID(ctx context.Context, ids []int) ([]Inventory, error) {
	return listByIDs[Inventory](ctx, c, "/api/v2/inventories/", ids)
}

// ProjectsByID reads a set of projects by id.
func (c *Client) ProjectsByID(ctx context.Context, ids []int) ([]Project, error) {
	return listByIDs[Project](ctx, c, "/api/v2/projects/", ids)
}

// SchedulesByID reads a set of schedules by id.
func (c *Client) SchedulesByID(ctx context.Context, ids []int) ([]Schedule, error) {
	return listByIDs[Schedule](ctx, c, "/api/v2/schedules/", ids)
}

// UnifiedJob re-reads one run of any kind, for the status and elapsed time a
// list row goes stale on.
func (c *Client) UnifiedJob(ctx context.Context, res Resource, id int) (Job, error) {
	var j Job
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v2/%s/%d/", res, id), nil, &j)
	// Real AWX always states the type; falling back to the collection we
	// asked about keeps a refreshed record pointing at its own output
	// endpoints rather than silently reverting to /api/v2/jobs/.
	if j.Type == "" {
		j.Type = res.recordType()
	}
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
	// Sources are what a sync updates. An inventory whose hosts are entered
	// by hand has none, and nothing to sync.
	TotalInventorySources       int `json:"total_inventory_sources"`
	InventorySourcesWithFailure int `json:"inventory_sources_with_failures"`
	SummaryFields               struct {
		Organization struct {
			Name string `json:"name"`
		} `json:"organization"`
	} `json:"summary_fields"`
}

// Inventories returns a page of inventories.
func (c *Client) Inventories(ctx context.Context, pageURL, search string) (Page[Inventory], error) {
	return listPage[Inventory](ctx, c, firstOr(pageURL, listURL("/api/v2/inventories/", "name", PageSize, search)))
}

// allPages walks a paged endpoint to its end, up to maxPages, for callers that
// need a complete list rather than a screenful. Partial results are returned
// alongside an error so a failure on page three still yields pages one and two.
func allPages[T any](ctx context.Context, maxPages int, page func(context.Context, string, string) (Page[T], error)) ([]T, error) {
	var out []T
	next := ""
	for i := 0; i < maxPages; i++ {
		p, err := page(ctx, next, "")
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

// AllInventories walks every page of inventories.
func (c *Client) AllInventories(ctx context.Context, maxPages int) ([]Inventory, error) {
	return allPages(ctx, maxPages, c.Inventories)
}

// InstanceGroup is a pool of execution nodes a job can be pinned to.
type InstanceGroup struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	IsContainerGroup bool   `json:"is_container_group"`
}

// InstanceGroups returns a page of instance groups.
func (c *Client) InstanceGroups(ctx context.Context, pageURL, search string) (Page[InstanceGroup], error) {
	return listPage[InstanceGroup](ctx, c, firstOr(pageURL, listURL("/api/v2/instance_groups/", "name", PageSize, search)))
}

// AllInstanceGroups walks every page of instance groups.
func (c *Client) AllInstanceGroups(ctx context.Context, maxPages int) ([]InstanceGroup, error) {
	return allPages(ctx, maxPages, c.InstanceGroups)
}

// Label is a free-form tag attached to a job.
type Label struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	SummaryFields struct {
		Organization struct {
			Name string `json:"name"`
		} `json:"organization"`
	} `json:"summary_fields"`
}

// Labels returns a page of labels.
func (c *Client) Labels(ctx context.Context, pageURL, search string) (Page[Label], error) {
	return listPage[Label](ctx, c, firstOr(pageURL, listURL("/api/v2/labels/", "name", PageSize, search)))
}

// AllLabels walks every page of labels.
func (c *Client) AllLabels(ctx context.Context, maxPages int) ([]Label, error) {
	return allPages(ctx, maxPages, c.Labels)
}

// ExecutionEnvironment is the container image a job runs inside.
type ExecutionEnvironment struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
}

// ExecutionEnvironments returns a page of execution environments.
func (c *Client) ExecutionEnvironments(ctx context.Context, pageURL, search string) (Page[ExecutionEnvironment], error) {
	return listPage[ExecutionEnvironment](ctx, c, firstOr(pageURL, listURL("/api/v2/execution_environments/", "name", PageSize, search)))
}

// AllExecutionEnvironments walks every page of execution environments.
func (c *Client) AllExecutionEnvironments(ctx context.Context, maxPages int) ([]ExecutionEnvironment, error) {
	return allPages(ctx, maxPages, c.ExecutionEnvironments)
}

// Credential is a machine, vault or cloud credential a job can use.
type Credential struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	SummaryFields struct {
		CredentialType struct {
			Name string `json:"name"`
		} `json:"credential_type"`
	} `json:"summary_fields"`
}

// TypeName is the credential's type for display, preferring the human name
// AWX puts in summary_fields over the terse kind.
func (c Credential) TypeName() string {
	if n := strings.TrimSpace(c.SummaryFields.CredentialType.Name); n != "" {
		return n
	}
	return c.Kind
}

// Credentials returns a page of credentials.
func (c *Client) Credentials(ctx context.Context, pageURL, search string) (Page[Credential], error) {
	return listPage[Credential](ctx, c, firstOr(pageURL, listURL("/api/v2/credentials/", "name", PageSize, search)))
}

// AllCredentials walks every page of credentials.
func (c *Client) AllCredentials(ctx context.Context, maxPages int) ([]Credential, error) {
	return allPages(ctx, maxPages, c.Credentials)
}

// Project is a source of playbooks. The list endpoint already returns every
// field the details view needs.
type Project struct {
	ID                    int        `json:"id"`
	Name                  string     `json:"name"`
	Description           string     `json:"description"`
	SCMType               string     `json:"scm_type"`
	SCMURL                string     `json:"scm_url"`
	SCMBranch             string     `json:"scm_branch"`
	SCMRefspec            string     `json:"scm_refspec"`
	SCMRevision           string     `json:"scm_revision"`
	SCMClean              bool       `json:"scm_clean"`
	SCMDeleteOnUpdate     bool       `json:"scm_delete_on_update"`
	SCMTrackSubmodules    bool       `json:"scm_track_submodules"`
	SCMUpdateOnLaunch     bool       `json:"scm_update_on_launch"`
	SCMUpdateCacheTimeout int        `json:"scm_update_cache_timeout"`
	AllowOverride         bool       `json:"allow_override"`
	Timeout               int        `json:"timeout"`
	LocalPath             string     `json:"local_path"`
	Status                string     `json:"status"`
	LastUpdated           *time.Time `json:"last_updated"`
	Created               *time.Time `json:"created"`
	SummaryFields         struct {
		Organization struct {
			Name string `json:"name"`
		} `json:"organization"`
		Credential struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"credential"`
	} `json:"summary_fields"`
}

// SCMTypeLabel names the source. AWX leaves scm_type empty for a project
// whose playbooks are managed on disk rather than pulled from source control.
func (p Project) SCMTypeLabel() string {
	if p.SCMType == "" {
		return "manual"
	}
	return p.SCMType
}

// Projects returns a page of projects.
func (c *Client) Projects(ctx context.Context, pageURL, search string) (Page[Project], error) {
	return listPage[Project](ctx, c, firstOr(pageURL, listURL("/api/v2/projects/", "name", PageSize, search)))
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
func (c *Client) Hosts(ctx context.Context, inventoryID int, pageURL, search string) (Page[Host], error) {
	return listPage[Host](ctx, c, firstOr(pageURL,
		listURL(fmt.Sprintf("/api/v2/inventories/%d/hosts/", inventoryID), "name", PageSize, search)))
}

// Cancel requests cancellation of a running job, project update or sync.
func (c *Client) Cancel(ctx context.Context, res Resource, id int) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v2/%s/%d/cancel/", res, id), strings.NewReader("{}"), nil)
}

// ErrStdoutNotReady is returned when AWX accepts the stdout request but has
// not finished collating the job's output yet (HTTP 202). This is normal for
// jobs that are still running; read JobEvents instead.
var ErrStdoutNotReady = errors.New("job stdout is not ready yet")

// Stdout returns the whole output of a run as ANSI-coloured text. AWX only
// serves this reliably once a run has finished and its events are processed.
func (c *Client) Stdout(ctx context.Context, res Resource, id int) (string, error) {
	q := url.Values{"format": {"ansi"}}
	path := fmt.Sprintf("/api/v2/%s/%d/stdout/?%s", res, id, q.Encode())
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := c.send(req, nil)
	if err != nil {
		return "", c.redactErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		return "", ErrStdoutNotReady
	}
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", c.redactErr(fmt.Errorf("GET %s: %s: %s",
			path, resp.Status, strings.TrimSpace(string(msg))))
	}
	raw, err := io.ReadAll(resp.Body)
	return string(raw), err
}

// EventPageSize is how many job events are fetched per request.
const EventPageSize = 200

// JobEvent is one entry of a run's output stream. This is the same data the
// AWX web UI renders, and unlike /stdout/ it is available while a run is in
// progress. Playbook jobs, project updates and inventory syncs each serve
// their own events, in the same shape.
type JobEvent struct {
	Counter   int    `json:"counter"`
	Stdout    string `json:"stdout"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// JobEvents returns up to EventPageSize events with a counter above after,
// oldest first.
func (c *Client) JobEvents(ctx context.Context, res Resource, id, after int) ([]JobEvent, error) {
	path := fmt.Sprintf("/api/v2/%s/%d/%s/?order_by=counter&page_size=%d&counter__gt=%d",
		res, id, res.eventsPath(), EventPageSize, after)
	return list[JobEvent](ctx, c, path)
}
