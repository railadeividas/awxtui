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
	connectedMsg   struct{ user string }
	templatesMsg   []awx.JobTemplate
	jobsMsg        []awx.Job
	inventoriesMsg []awx.Inventory
	projectsMsg    []awx.Project
	hostsMsg       struct {
		inventory string
		hosts     []awx.Host
	}
	// outputMsg carries a batch of job output. chunk holds the events newer
	// than the counter that was requested; reset means replace, not append.
	outputMsg struct {
		job     awx.Job
		chunk   string
		counter int
		reset   bool
		more    bool
	}
	launchedMsg struct{ job awx.Job }
	canceledMsg struct{ id int }
	errMsg      struct{ err error }
	tickMsg     time.Time
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
		return errMsg{err}
	}
	return connectedMsg{user}
}

func (m Model) fetch(t tab) tea.Cmd {
	c := m.client
	switch t {
	case tabTemplates:
		return func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			v, err := c.JobTemplates(ctx)
			if err != nil {
				return errMsg{err}
			}
			return templatesMsg(v)
		}
	case tabJobs:
		return func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			v, err := c.Jobs(ctx)
			if err != nil {
				return errMsg{err}
			}
			return jobsMsg(v)
		}
	case tabInventories:
		return func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			v, err := c.Inventories(ctx)
			if err != nil {
				return errMsg{err}
			}
			return inventoriesMsg(v)
		}
	case tabProjects:
		return func() tea.Msg {
			ctx, cancel := cmdCtx()
			defer cancel()
			v, err := c.Projects(ctx)
			if err != nil {
				return errMsg{err}
			}
			return projectsMsg(v)
		}
	}
	return nil
}

func (m Model) fetchHosts(inventoryID int, name string) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		v, err := c.Hosts(ctx, inventoryID)
		if err != nil {
			return errMsg{err}
		}
		return hostsMsg{inventory: name, hosts: v}
	}
}

// fetchOutput reads job output. A finished job is fetched whole from the
// /stdout/ endpoint in one request; a running job is tailed through
// job_events, which — unlike /stdout/ — is populated while it runs. Passing
// after > 0 fetches only the events newer than that counter.
func (m Model) fetchOutput(jobID, after int) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		job, err := c.Job(ctx, jobID)
		if err != nil {
			return errMsg{err}
		}

		if after == 0 && !job.IsRunning() {
			text, err := c.Stdout(ctx, jobID)
			switch {
			case err == nil && strings.TrimSpace(text) != "":
				return outputMsg{job: job, chunk: text, reset: true}
			case err != nil && !errors.Is(err, awx.ErrStdoutNotReady):
				// Fall through to job_events, which may still have the output.
			}
		}

		events, err := c.JobEvents(ctx, jobID, after)
		if err != nil {
			return errMsg{err}
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
		}
	}
}

func (m Model) launch(templateID int, extraVars string) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		job, err := c.Launch(ctx, templateID, extraVars)
		if err != nil {
			return errMsg{err}
		}
		return launchedMsg{job}
	}
}

func (m Model) cancelJob(jobID int) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		if err := c.Cancel(ctx, jobID); err != nil {
			return errMsg{err}
		}
		return canceledMsg{jobID}
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
			},
			search: strings.ToLower(inv.Name + " " + inv.SummaryFields.Organization.Name),
		})
	}
	return rows
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
