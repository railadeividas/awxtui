package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

type (
	connectedMsg struct {
		user string
		gen  int
	}

	// pageMeta describes the page a list message carries. cont marks a
	// continuation of a list already on screen; query is the server-side
	// search it reflects; seq lets stale replies be dropped.
	pageMeta struct {
		next  string
		count int
		cont  bool
		query string
		seq   int
		gen   int
	}
	templatesMsg struct {
		pageMeta
		items []awx.JobTemplate
	}
	jobsMsg struct {
		pageMeta
		items []awx.Job
	}
	inventoriesMsg struct {
		pageMeta
		items []awx.Inventory
	}
	projectsMsg struct {
		pageMeta
		items []awx.Project
	}
	// playbooksMsg carries the playbook names of one project. It carries its
	// own error so the details view can stop saying "loading…" even when the
	// endpoint fails.
	playbooksMsg struct {
		gen       int
		projectID int
		names     []string
		err       error
	}
	hostsMsg struct {
		pageMeta
		inventory   string
		inventoryID int
		hosts       []awx.Host
	}
	// searchTickMsg fires after the user stops typing, so a search is sent
	// once rather than on every keystroke.
	searchTickMsg struct {
		tab tab
		seq int
	}
	outputMsg struct {
		job     awx.Job
		chunk   string
		counter int
		reset   bool
		more    bool
		gen     int
	}
	launchedMsg struct {
		job awx.Job
		gen int
	}
	// launchFormMsg carries everything needed to build the launch form.
	launchFormMsg struct {
		gen            int
		template       awx.JobTemplate
		config         awx.LaunchConfig
		survey         awx.SurveySpec
		inventories    []awx.Inventory
		credentials    []awx.Credential
		environments   []awx.ExecutionEnvironment
		instanceGroups []awx.InstanceGroup
		labels         []awx.Label
	}
	canceledMsg struct {
		id  int
		gen int
	}
	// syncedMsg reports that a project update or inventory sync has started.
	// An inventory with several sources starts one update per source, so
	// started holds them all; the first is the one whose output opens.
	syncedMsg struct {
		gen     int
		what    string
		started []awx.Job
	}
	errMsg struct {
		err error
		gen int
	}
	tickMsg time.Time
)

func (e errMsg) Error() string { return e.err.Error() }

func cmdCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func (m Model) connect() tea.Msg {
	ctx, cancel := cmdCtx()
	defer cancel()
	user, err := m.client.Me(ctx)
	if err != nil {
		return errMsg{err: err, gen: m.gen}
	}
	return connectedMsg{user: user, gen: m.gen}
}

// fetch requests one page of a list. An empty pageURL starts at the first
// page, applying search server-side; cont marks the result as a continuation
// to append. A page URL already carries the search it belongs to.
func (m Model) fetch(t tab, pageURL, search string, cont bool) tea.Cmd {
	c, gen := m.client, m.gen
	meta := pageMeta{cont: cont, query: strings.TrimSpace(search), seq: m.searchSeq[t], gen: m.gen}
	switch t {
	case tabTemplates:
		return func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			p, err := c.JobTemplates(ctx, pageURL, search)
			if err != nil {
				return errMsg{err: err, gen: gen}
			}
			meta.next, meta.count = p.Next, p.Count
			return templatesMsg{pageMeta: meta, items: p.Results}
		}
	case tabJobs:
		return func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			p, err := c.Jobs(ctx, pageURL, search)
			if err != nil {
				return errMsg{err: err, gen: gen}
			}
			meta.next, meta.count = p.Next, p.Count
			return jobsMsg{pageMeta: meta, items: p.Results}
		}
	case tabInventories:
		return func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			p, err := c.Inventories(ctx, pageURL, search)
			if err != nil {
				return errMsg{err: err, gen: gen}
			}
			meta.next, meta.count = p.Next, p.Count
			return inventoriesMsg{pageMeta: meta, items: p.Results}
		}
	case tabProjects:
		return func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			p, err := c.Projects(ctx, pageURL, search)
			if err != nil {
				return errMsg{err: err, gen: gen}
			}
			meta.next, meta.count = p.Next, p.Count
			return projectsMsg{pageMeta: meta, items: p.Results}
		}
	}
	return nil
}

// searchDebounce waits for typing to settle before querying AWX.
func searchDebounce(t tab, seq int) tea.Cmd {
	return tea.Tick(searchDelay, func(time.Time) tea.Msg {
		return searchTickMsg{tab: t, seq: seq}
	})
}

func (m Model) fetchPlaybooks(projectID int) tea.Cmd {
	c, gen := m.client, m.gen
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		names, err := c.ProjectPlaybooks(ctx, projectID)
		return playbooksMsg{gen: gen, projectID: projectID, names: names, err: err}
	}
}

func (m Model) fetchHosts(inventoryID int, name, pageURL string, cont bool) tea.Cmd {
	c, gen := m.client, m.gen
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		p, err := c.Hosts(ctx, inventoryID, pageURL, "")
		if err != nil {
			return errMsg{err: err, gen: gen}
		}
		return hostsMsg{
			pageMeta:  pageMeta{next: p.Next, count: p.Count, cont: cont, gen: gen},
			inventory: name, inventoryID: inventoryID, hosts: p.Results,
		}
	}
}

// fetchOutput reads the output of a run. A finished one is fetched whole from
// the /stdout/ endpoint in one request; a running one is tailed through its
// events, which — unlike /stdout/ — are populated while it runs. Passing
// after > 0 fetches only the events newer than that counter.
//
// res says which collection to read: a project update and an inventory sync
// keep their output under their own endpoints, not under /api/v2/jobs/.
func (m Model) fetchOutput(res awx.Resource, jobID, after int) tea.Cmd {
	c, gen := m.client, m.gen
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		job, err := c.UnifiedJob(ctx, res, jobID)
		if err != nil {
			return errMsg{err: err, gen: gen}
		}

		if after == 0 && !job.IsRunning() {
			text, err := c.Stdout(ctx, res, jobID)
			switch {
			case err == nil && strings.TrimSpace(text) != "":
				return outputMsg{job: job, chunk: text, reset: true, gen: gen}
			case err != nil && !errors.Is(err, awx.ErrStdoutNotReady):
				// Fall through to the events, which may still have the output.
			}
		}

		events, err := c.JobEvents(ctx, res, jobID, after)
		if err != nil {
			return errMsg{err: err, gen: gen}
		}
		var b strings.Builder
		last := after
		for _, e := range events {
			if e.Counter > last {
				last = e.Counter
			}
			if line := strings.TrimRight(e.Stdout, "\r\n"); line != "" {
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
		return outputMsg{
			job:     job,
			chunk:   b.String(),
			counter: last,
			reset:   after == 0,
			more:    len(events) == awx.EventPageSize,
			gen:     gen,
		}
	}
}

func (m Model) launch(templateID int, payload map[string]any) tea.Cmd {
	c, gen := m.client, m.gen
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		job, err := c.Launch(ctx, templateID, payload)
		if err != nil {
			return errMsg{err: err, gen: gen}
		}
		return launchedMsg{job: job, gen: gen}
	}
}

// fetchLaunchForm reads what a template needs before it can start: the
// ask_*_on_launch prompts, its survey, and an inventory list when the template
// lets the user choose one.
func (m Model) fetchLaunchForm(t awx.JobTemplate) tea.Cmd {
	c, gen := m.client, m.gen
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		cfg, err := c.LaunchConfig(ctx, t.ID)
		if err != nil {
			return errMsg{err: err, gen: gen}
		}
		msg := launchFormMsg{gen: gen, template: t, config: cfg}
		if cfg.SurveyEnabled {
			spec, err := c.SurveySpec(ctx, t.ID)
			if err != nil {
				return errMsg{err: err, gen: gen}
			}
			msg.survey = spec
		}
		if cfg.AskInventory {
			// Best effort: the form falls back to typing an id.
			msg.inventories, _ = c.AllInventories(ctx, maxPages)
		}
		// The pick-from-a-list prompts each need their own catalogue. All are
		// best effort: without a list the form omits the prompt and AWX uses
		// the template's own value, which is what happens today anyway.
		if cfg.AskCredentials {
			msg.credentials, _ = c.AllCredentials(ctx, maxPages)
		}
		if cfg.AskExecutionEnvironment {
			msg.environments, _ = c.AllExecutionEnvironments(ctx, maxPages)
		}
		if cfg.AskInstanceGroups {
			msg.instanceGroups, _ = c.AllInstanceGroups(ctx, maxPages)
		}
		if cfg.AskLabels {
			msg.labels, _ = c.AllLabels(ctx, maxPages)
		}
		return msg
	}
}

func (m Model) cancelJob(res awx.Resource, jobID int) tea.Cmd {
	c, gen := m.client, m.gen
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		if err := c.Cancel(ctx, res, jobID); err != nil {
			return errMsg{err: err, gen: gen}
		}
		return canceledMsg{id: jobID, gen: gen}
	}
}

// syncProject starts an SCM update of one project.
func (m Model) syncProject(p awx.Project) tea.Cmd {
	c, gen := m.client, m.gen
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		job, err := c.UpdateProject(ctx, p.ID)
		if err != nil {
			return errMsg{err: fmt.Errorf("sync project %s: %w", p.Name, err), gen: gen}
		}
		return syncedMsg{gen: gen, what: "project " + p.Name, started: []awx.Job{job}}
	}
}

// syncInventory starts a sync of every source of one inventory.
func (m Model) syncInventory(inv awx.Inventory) tea.Cmd {
	c, gen := m.client, m.gen
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		started, err := c.SyncInventory(ctx, inv.ID)
		if err != nil {
			// Some sources may already have started before one failed; say so
			// rather than implying nothing happened.
			wrapped := fmt.Errorf("sync inventory %s: %w", inv.Name, err)
			if len(started) > 0 {
				wrapped = fmt.Errorf("%w (%d source(s) already started)", wrapped, len(started))
			}
			return errMsg{err: wrapped, gen: gen}
		}
		return syncedMsg{gen: gen, what: "inventory " + inv.Name, started: started}
	}
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ---- row builders ----

func templateRows(items []awx.JobTemplate) []row {
	rows := make([]row, 0, len(items))
	for _, t := range items {
		last := statusBadge(t.SummaryFields.LastJob.Status)
		when := dimStyle.Render("—")
		if t.LastJobRun != nil {
			when = dimStyle.Render(ago(*t.LastJobRun))
		}
		rows = append(rows, row{
			id: t.ID,
			cells: []string{
				t.Name,
				dimStyle.Render(t.SummaryFields.Project.Name),
				dimStyle.Render(t.SummaryFields.Inventory.Name),
				last,
				when,
			},
			search: strings.ToLower(t.Name + " " + t.SummaryFields.Project.Name + " " + t.SummaryFields.Inventory.Name),
		})
	}
	return rows
}

func jobRows(items []awx.Job) []row {
	rows := make([]row, 0, len(items))
	for _, j := range items {
		when := dimStyle.Render("—")
		if j.Started != nil {
			when = dimStyle.Render(ago(*j.Started))
		}
		rows = append(rows, row{
			id: j.ID,
			cells: []string{
				dimStyle.Render(fmt.Sprintf("#%d", j.ID)),
				j.Name,
				statusBadge(j.Status),
				dimStyle.Render(duration(j.Elapsed)),
				when,
				dimStyle.Render(j.SummaryFields.CreatedBy.Username),
			},
			search: strings.ToLower(fmt.Sprintf("%d %s %s %s", j.ID, j.Name, j.Status, j.SummaryFields.CreatedBy.Username)),
		})
	}
	return rows
}

func inventoryRows(items []awx.Inventory) []row {
	rows := make([]row, 0, len(items))
	for _, inv := range items {
		health := okStyle.Render("healthy")
		if inv.HostsWithActiveFailures > 0 {
			health = errStyle.Render(fmt.Sprintf("%d failing", inv.HostsWithActiveFailures))
		} else if inv.TotalHosts == 0 {
			health = dimStyle.Render("empty")
		}
		rows = append(rows, row{
			id: inv.ID,
			cells: []string{
				inv.Name,
				dimStyle.Render(inv.SummaryFields.Organization.Name),
				dimStyle.Render(fmt.Sprintf("%d", inv.TotalHosts)),
				dimStyle.Render(fmt.Sprintf("%d", inv.TotalGroups)),
				health,
				inventorySourceCell(inv),
			},
			search: strings.ToLower(inv.Name + " " + inv.SummaryFields.Organization.Name),
		})
	}
	return rows
}

// inventorySourceCell shows how many sources a sync would update, so that an
// inventory whose hosts were entered by hand reads as "nothing to sync"
// rather than as a sync that did nothing.
func inventorySourceCell(inv awx.Inventory) string {
	if inv.TotalInventorySources == 0 {
		return dimStyle.Render("—")
	}
	n := fmt.Sprintf("%d", inv.TotalInventorySources)
	if inv.InventorySourcesWithFailure > 0 {
		return errStyle.Render(n + " ✗")
	}
	return dimStyle.Render(n)
}

func projectRows(items []awx.Project) []row {
	rows := make([]row, 0, len(items))
	for _, p := range items {
		when := dimStyle.Render("—")
		if p.LastUpdated != nil {
			when = dimStyle.Render(ago(*p.LastUpdated))
		}
		scm := p.SCMType
		if scm == "" {
			scm = "manual"
		}
		branch := p.SCMBranch
		if branch == "" {
			branch = "—"
		}
		rows = append(rows, row{
			id: p.ID,
			cells: []string{
				p.Name,
				dimStyle.Render(scm),
				dimStyle.Render(branch),
				statusBadge(p.Status),
				when,
			},
			search: strings.ToLower(p.Name + " " + scm + " " + branch),
		})
	}
	return rows
}

func hostRows(items []awx.Host) []row {
	rows := make([]row, 0, len(items))
	for _, h := range items {
		state := okStyle.Render("ok")
		if h.HasActiveFailures {
			state = errStyle.Render("failed")
		}
		if !h.Enabled {
			state = dimStyle.Render("disabled")
		}
		rows = append(rows, row{
			id: h.ID,
			cells: []string{
				h.Name,
				state,
				dimStyle.Render(h.Description),
			},
			search: strings.ToLower(h.Name + " " + h.Description),
		})
	}
	return rows
}

// ago formats a timestamp as a short relative age.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// duration formats AWX's fractional elapsed seconds.
func duration(sec float64) string {
	if sec <= 0 {
		return "—"
	}
	d := time.Duration(sec * float64(time.Second))
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
