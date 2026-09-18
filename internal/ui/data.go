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
		user awx.User
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
	schedulesMsg struct {
		pageMeta
		items []awx.Schedule
	}
	// scheduleToggledMsg reports the outcome of a schedule enabled/disabled
	// PATCH, carrying the value that was requested rather than re-fetching it.
	scheduleToggledMsg struct {
		id      int
		enabled bool
		err     error
		gen     int
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
	// errMsg carries the tab whose request failed, so its fetching/searching
	// state can be reset; tab is tabCount for requests not tied to a list.
	errMsg struct {
		err error
		gen int
		tab tab
	}
	tickMsg time.Time
)

// trackedMsg is the reply to a counted request, on its way back to Update.
// Wrapping it is what pairs the release with the request: the counter goes up
// in request and comes down in exactly one place, however the reply turned
// out — a result, an error, or a stale generation that gets dropped.
type trackedMsg struct{ inner tea.Msg }

func (e errMsg) Error() string { return e.err.Error() }

func cmdCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// request counts cmd as one call to AWX for as long as it takes to answer,
// and marks its reply so Update can release it again. Every command that
// talks to AWX is built by a method that goes through here, so the counter
// cannot be forgotten: it used to be raised by hand at eight of the twenty-odd
// places that issue a request, which left paging, refreshes, syncs, cancels
// and output polls all running with the corner badge still reading
// "connected".
func (m *Model) request(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	m.inflight++
	return func() tea.Msg { return trackedMsg{inner: cmd()} }
}

func (m Model) connect() tea.Msg {
	ctx, cancel := cmdCtx()
	defer cancel()
	user, err := m.client.Me(ctx)
	if err != nil {
		return errMsg{err: err, gen: m.gen, tab: tabCount}
	}
	return connectedMsg{user: user, gen: m.gen}
}

// fetch requests one page of a list. An empty pageURL starts at the first
// page, applying search server-side; cont marks the result as a continuation
// to append. A page URL already carries the search it belongs to.
func (m *Model) fetch(t tab, pageURL, search string, cont bool) tea.Cmd {
	c, gen := m.client, m.gen
	meta := pageMeta{cont: cont, query: strings.TrimSpace(search), seq: m.searchSeq[t], gen: m.gen}
	// A pinned-only view is not a page of a list: it is a named set of ids,
	// read in one request and never paged.
	if m.show[t].pinnedOnly {
		return m.request(m.pinnedCmd(t, meta))
	}
	switch t {
	case tabTemplates:
		return m.request(func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			p, err := c.JobTemplates(ctx, pageURL, search)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabTemplates}
			}
			meta.next, meta.count = p.Next, p.Count
			return templatesMsg{pageMeta: meta, items: p.Results}
		})
	case tabJobs:
		// /api/v2/jobs/ holds playbook jobs alone, so a project update or an
		// inventory sync would be missing from it entirely. The unified list
		// holds all three and takes the same filters.
		filter := m.jobFilter()
		return m.request(func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			p, err := c.UnifiedJobs(ctx, pageURL, search, filter)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabJobs}
			}
			meta.next, meta.count = p.Next, p.Count
			return jobsMsg{pageMeta: meta, items: p.Results}
		})
	case tabInventories:
		return m.request(func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			p, err := c.Inventories(ctx, pageURL, search)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabInventories}
			}
			meta.next, meta.count = p.Next, p.Count
			return inventoriesMsg{pageMeta: meta, items: p.Results}
		})
	case tabProjects:
		return m.request(func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			p, err := c.Projects(ctx, pageURL, search)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabProjects}
			}
			meta.next, meta.count = p.Next, p.Count
			return projectsMsg{pageMeta: meta, items: p.Results}
		})
	case tabSchedules:
		return m.request(func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			p, err := c.Schedules(ctx, pageURL, search)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabSchedules}
			}
			meta.next, meta.count = p.Next, p.Count
			return schedulesMsg{pageMeta: meta, items: p.Results}
		})
	}
	return nil
}

// jobFilter turns what the Jobs tab is showing into the query AWX runs.
func (m Model) jobFilter() awx.JobFilter {
	f := awx.JobFilter{Status: m.show[tabJobs].status, Type: m.show[tabJobs].kind}
	if m.show[tabJobs].mine {
		f.CreatedBy = m.userID
	}
	return f
}

// fetchPinned reads one tab's pinned records in a single request. The ids
// come from disk, so the list survives a restart; the records come from AWX,
// so their status is current. Records AWX no longer has come back missing
// rather than stale, and the count says so.
func (m Model) pinnedCmd(t tab, meta pageMeta) tea.Cmd {
	c, gen := m.client, m.gen
	ids := m.store.IDs(m.instance, pinGroup(t))
	meta.count = len(ids)
	keep := m.show[t]
	me := m.user
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		switch t {
		case tabJobs:
			found, err := c.UnifiedJobsByID(ctx, ids)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabJobs}
			}
			// The other facets are applied here rather than by AWX: a pinned
			// set is small and already in hand, and id__in plus a status
			// filter would hide a pin instead of listing it as it is.
			var kept []awx.Job
			for _, j := range found {
				if keep.status != "" && j.Status != keep.status {
					continue
				}
				if keep.kind != "" && j.Type != keep.kind {
					continue
				}
				if keep.mine && j.SummaryFields.CreatedBy.Username != me {
					continue
				}
				kept = append(kept, j)
			}
			return jobsMsg{pageMeta: meta, items: inPinnedOrder(ids, kept, jobID)}
		case tabTemplates:
			found, err := c.JobTemplatesByID(ctx, ids)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabTemplates}
			}
			return templatesMsg{pageMeta: meta, items: inPinnedOrder(ids, found, templateID)}
		case tabInventories:
			found, err := c.InventoriesByID(ctx, ids)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabInventories}
			}
			return inventoriesMsg{pageMeta: meta, items: inPinnedOrder(ids, found, inventoryID)}
		case tabSchedules:
			found, err := c.SchedulesByID(ctx, ids)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabSchedules}
			}
			return schedulesMsg{pageMeta: meta, items: inPinnedOrder(ids, found, scheduleID)}
		default:
			found, err := c.ProjectsByID(ctx, ids)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabProjects}
			}
			return projectsMsg{pageMeta: meta, items: inPinnedOrder(ids, found, projectID)}
		}
	}
}

// searchDebounce waits for typing to settle before querying AWX.
func searchDebounce(t tab, seq int) tea.Cmd {
	return tea.Tick(searchDelay, func(time.Time) tea.Msg {
		return searchTickMsg{tab: t, seq: seq}
	})
}

func (m *Model) fetchHosts(inventoryID int, name, pageURL string, cont bool) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		p, err := c.Hosts(ctx, inventoryID, pageURL, "")
		if err != nil {
			return errMsg{err: err, gen: gen, tab: tabCount}
		}
		return hostsMsg{
			pageMeta:  pageMeta{next: p.Next, count: p.Count, cont: cont, gen: gen},
			inventory: name, inventoryID: inventoryID, hosts: p.Results,
		}
	})
}

// fetchOutput reads the output of a run. A finished one is fetched whole from
// the /stdout/ endpoint in one request; a running one is tailed through its
// events, which — unlike /stdout/ — are populated while it runs. Passing
// after > 0 fetches only the events newer than that counter.
//
// res says which collection to read: a project update and an inventory sync
// keep their output under their own endpoints, not under /api/v2/jobs/.
func (m *Model) fetchOutput(res awx.Resource, jobID, after int) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		job, err := c.UnifiedJob(ctx, res, jobID)
		if err != nil {
			return errMsg{err: err, gen: gen, tab: tabCount}
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
			return errMsg{err: err, gen: gen, tab: tabCount}
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
	})
}

func (m *Model) launch(templateID int, payload map[string]any) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		job, err := c.Launch(ctx, templateID, payload)
		if err != nil {
			return errMsg{err: err, gen: gen, tab: tabCount}
		}
		return launchedMsg{job: job, gen: gen}
	})
}

// fetchLaunchForm reads what a template needs before it can start: the
// ask_*_on_launch prompts, its survey, and an inventory list when the template
// lets the user choose one.
func (m *Model) fetchLaunchForm(t awx.JobTemplate) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		cfg, err := c.LaunchConfig(ctx, t.ID)
		if err != nil {
			return errMsg{err: err, gen: gen, tab: tabCount}
		}
		msg := launchFormMsg{gen: gen, template: t, config: cfg}
		if cfg.SurveyEnabled {
			spec, err := c.SurveySpec(ctx, t.ID)
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabCount}
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
	})
}

func (m *Model) cancelJob(res awx.Resource, jobID int) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		if err := c.Cancel(ctx, res, jobID); err != nil {
			return errMsg{err: err, gen: gen, tab: tabCount}
		}
		return canceledMsg{id: jobID, gen: gen}
	})
}

// syncProject starts an SCM update of one project.
func (m *Model) syncProject(p awx.Project) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		job, err := c.UpdateProject(ctx, p.ID)
		if err != nil {
			return errMsg{err: fmt.Errorf("sync project %s: %w", p.Name, err), gen: gen, tab: tabCount}
		}
		return syncedMsg{gen: gen, what: "project " + p.Name, started: []awx.Job{job}}
	})
}

// syncInventory starts a sync of every source of one inventory.
func (m *Model) syncInventory(inv awx.Inventory) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
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
			return errMsg{err: wrapped, gen: gen, tab: tabCount}
		}
		return syncedMsg{gen: gen, what: "inventory " + inv.Name, started: started}
	})
}

// toggleScheduleEnabled flips a schedule's enabled flag.
func (m *Model) toggleScheduleEnabled(s awx.Schedule) tea.Cmd {
	c, gen := m.client, m.gen
	enabled := !s.Enabled
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		if err := c.SetScheduleEnabled(ctx, s.ID, enabled); err != nil {
			return scheduleToggledMsg{id: s.ID, enabled: s.Enabled, err: fmt.Errorf("toggle schedule %s: %w", s.Name, err), gen: gen}
		}
		return scheduleToggledMsg{id: s.ID, enabled: enabled, gen: gen}
	})
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ---- row builders ----

func (m Model) templateRows(items []awx.JobTemplate) []row {
	rows := make([]row, 0, len(items))
	for _, t := range items {
		pinned := ""
		if m.pinned(tabTemplates, t.ID) {
			pinned = "pinned"
		}
		last := statusBadge(t.SummaryFields.LastJob.Status)
		when := dimStyle.Render("—")
		if t.LastJobRun != nil {
			when = dimStyle.Render(ago(*t.LastJobRun))
		}
		rows = append(rows, row{
			id: t.ID,
			cells: []string{
				m.pinMark(tabTemplates, t.ID, t.Name),
				dimStyle.Render(t.SummaryFields.Project.Name),
				dimStyle.Render(t.SummaryFields.Inventory.Name),
				last,
				when,
			},
			search: strings.ToLower(t.Name + " " + t.SummaryFields.Project.Name + " " +
				t.SummaryFields.Inventory.Name + " " + pinned),
		})
	}
	return rows
}

// jobRows renders the Jobs tab. Two things about a row matter beyond what
// AWX says about the job: whether it is pinned, and whether it is yours. Both
// are marked rather than filtered, so they are still visible in the
// unfiltered list where they are hardest to spot — the instance this was
// built against holds 170290 runs.
func (m Model) jobRows(items []awx.Job) []row {
	rows := make([]row, 0, len(items))
	for _, j := range items {
		when := dimStyle.Render("—")
		if j.Started != nil {
			when = dimStyle.Render(ago(*j.Started))
		}
		id, pinned := m.pinMark(tabJobs, j.ID, dimStyle.Render(fmt.Sprintf("#%d", j.ID))), ""
		if m.pinned(tabJobs, j.ID) {
			pinned = "pinned"
		}
		// The unified list mixes three kinds of run under one set of
		// columns, where a project update and a playbook job would otherwise
		// read identically.
		name := j.Name
		if j.IsSync() {
			name += dimStyle.Render(" · " + j.KindLabel())
		}
		// Your own runs say "you" rather than your username: a colour alone
		// would be invisible on a monochrome terminal, and scanning a column
		// of identical usernames for your own is the problem being solved.
		by := dimStyle.Render(j.SummaryFields.CreatedBy.Username)
		mine := ""
		if m.user != "" && j.SummaryFields.CreatedBy.Username == m.user {
			by, mine = mineStyle.Render("you"), "mine"
		}
		rows = append(rows, row{
			id:    j.ID,
			cells: []string{id, name, statusBadge(j.Status), dimStyle.Render(duration(j.Elapsed)), when, by},
			search: strings.ToLower(fmt.Sprintf("%d %s %s %s %s %s %s",
				j.ID, j.Name, j.Status, j.SummaryFields.CreatedBy.Username, j.KindLabel(), pinned, mine)),
		})
	}
	return rows
}

func (m Model) inventoryRows(items []awx.Inventory) []row {
	rows := make([]row, 0, len(items))
	for _, inv := range items {
		pinned := ""
		if m.pinned(tabInventories, inv.ID) {
			pinned = "pinned"
		}
		health := okStyle.Render("healthy")
		if inv.HostsWithActiveFailures > 0 {
			health = errStyle.Render(fmt.Sprintf("%d failing", inv.HostsWithActiveFailures))
		} else if inv.TotalHosts == 0 {
			health = dimStyle.Render("empty")
		}
		rows = append(rows, row{
			id: inv.ID,
			cells: []string{
				m.pinMark(tabInventories, inv.ID, inv.Name),
				dimStyle.Render(inv.SummaryFields.Organization.Name),
				dimStyle.Render(fmt.Sprintf("%d", inv.TotalHosts)),
				dimStyle.Render(fmt.Sprintf("%d", inv.TotalGroups)),
				health,
				inventorySourceCell(inv),
			},
			search: strings.ToLower(inv.Name + " " + inv.SummaryFields.Organization.Name + " " + pinned),
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

func (m Model) projectRows(items []awx.Project) []row {
	rows := make([]row, 0, len(items))
	for _, p := range items {
		pinned := ""
		if m.pinned(tabProjects, p.ID) {
			pinned = "pinned"
		}
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
				m.pinMark(tabProjects, p.ID, p.Name),
				dimStyle.Render(scm),
				dimStyle.Render(branch),
				statusBadge(p.Status),
				when,
			},
			search: strings.ToLower(p.Name + " " + scm + " " + branch + " " + pinned),
		})
	}
	return rows
}

func (m Model) scheduleRows(items []awx.Schedule) []row {
	rows := make([]row, 0, len(items))
	for _, s := range items {
		pinned := ""
		if m.pinned(tabSchedules, s.ID) {
			pinned = "pinned"
		}
		kind := scheduleKindLabel(s.SummaryFields.UnifiedJobTemplate.UnifiedJobType)
		next := dimStyle.Render("—")
		if s.NextRun != nil {
			next = dimStyle.Render(until(*s.NextRun))
		}
		enabled := okStyle.Render("enabled")
		if !s.Enabled {
			enabled = dimStyle.Render("disabled")
		}
		rows = append(rows, row{
			id: s.ID,
			cells: []string{
				m.pinMark(tabSchedules, s.ID, s.Name),
				dimStyle.Render(kind),
				dimStyle.Render(s.SummaryFields.UnifiedJobTemplate.Name),
				next,
				enabled,
			},
			search: strings.ToLower(s.Name + " " + kind + " " +
				s.SummaryFields.UnifiedJobTemplate.Name + " " + pinned),
		})
	}
	return rows
}

// scheduleKindLabel shortens AWX's unified_job_type into what fits a column,
// so a system-job schedule (cleanup, activity stream) reads apart from one
// backing an actual playbook.
func scheduleKindLabel(unifiedJobType string) string {
	switch unifiedJobType {
	case "job":
		return "template"
	case "project_update":
		return "project"
	case "inventory_update":
		return "inventory"
	case "system_job":
		return "system"
	case "workflow_job":
		return "workflow"
	default:
		return unifiedJobType
	}
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

// ago formats a timestamp as a short relative age. It assumes a past
// timestamp: time.Since on a future one comes back negative, which every
// branch here would read as "just now".
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

// until formats a timestamp still to come as a short relative time, the
// mirror of ago for a schedule's next run.
func until(t time.Time) string {
	d := time.Until(t)
	switch {
	case d < time.Minute:
		return "due now"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("in %dd", int(d.Hours()/24))
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
