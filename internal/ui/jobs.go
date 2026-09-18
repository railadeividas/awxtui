package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// jobDetail is the state of the job launch-details view. The jobs list's own
// record — from /api/v2/jobs/ or /api/v2/unified_jobs/ — omits extra_vars,
// tags and similar fields, so opening it always re-reads the record through
// UnifiedJob before showing it.
type jobDetail struct {
	job     awx.Job
	loading bool
	offset  int
}

// jobDetailMsg carries the full job record fetched for the details view.
type jobDetailMsg struct {
	gen int
	job awx.Job
}

// openJobDetail shows what a job was launched with.
func (m *Model) openJobDetail(j awx.Job) tea.Cmd {
	m.err, m.notice = nil, ""
	m.mode = modeJob
	m.job = jobDetail{job: j, loading: true}
	return m.fetchJobDetail(j)
}

func (m *Model) fetchJobDetail(j awx.Job) tea.Cmd {
	c, gen, res, id := m.client, m.gen, j.Resource(), j.ID
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		full, err := c.UnifiedJob(ctx, res, id)
		if err != nil {
			return errMsg{err: err, gen: gen, tab: tabCount}
		}
		return jobDetailMsg{gen: gen, job: full}
	})
}

func (m Model) handleJobKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc":
		m.mode = modeList
		m.job = jobDetail{}
		return m, nil
	case "up", "k":
		m.job.offset = m.clampJobOffset(m.job.offset - 1)
		return m, nil
	case "down", "j":
		m.job.offset = m.clampJobOffset(m.job.offset + 1)
		return m, nil
	case "pgup", "ctrl+u":
		m.job.offset = m.clampJobOffset(m.job.offset - m.jobWindow()/2)
		return m, nil
	case "pgdown", "ctrl+d":
		m.job.offset = m.clampJobOffset(m.job.offset + m.jobWindow()/2)
		return m, nil
	case "home", "g":
		m.job.offset = 0
		return m, nil
	case "end", "G":
		m.job.offset = m.clampJobOffset(1 << 30)
		return m, nil
	case "r":
		m.job.loading = true
		return m, m.fetchJobDetail(m.job.job)
	case "p":
		j := m.job.job
		return m, m.togglePin(tabJobs, j.ID, j.Name, j.Type)
	case "enter":
		j := m.job.job
		m.err, m.notice = nil, ""
		return m, m.openOutput(j)
	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
}

// jobWindow is how many body lines the modal can show at once. The modal
// spends lines on its border, padding and title; the key legend lives in the
// global status bar, the one place every view's legend appears.
func (m Model) jobWindow() int {
	return max(m.tableHeight()-5, 3)
}

func (m Model) clampJobOffset(off int) int {
	return clamp(off, 0, len(m.jobBody(m.jobWidth()))-m.jobWindow())
}

func (m Model) jobWidth() int {
	return min(m.width-8, 84)
}

// jobModal renders what a job was launched with.
func (m Model) jobModal() string {
	j := m.job.job
	width := m.jobWidth()
	lines := m.jobBody(width)
	window := m.jobWindow()
	off := clamp(m.job.offset, 0, max(len(lines)-window, 0))
	end := min(off+window, len(lines))

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Job #%d", j.ID)) + "  " + rowStyle.Bold(true).Render(j.Name))
	if len(lines) > window {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  (%d–%d of %d lines)", off+1, end, len(lines))))
	}
	b.WriteString("\n\n")
	b.WriteString(strings.Join(lines[off:end], "\n"))
	return modalStyle.Width(width).Render(b.String())
}

// jobBody is the scrollable content of the modal, one string per line.
func (m Model) jobBody(width int) []string {
	j := m.job.job
	valueW := max(width-20, 20)

	var lines []string
	field := func(label, value string) {
		if value == "" {
			value = dimStyle.Render("—")
		}
		wrapped := strings.Split(wrapANSI(value, valueW), "\n")
		lines = append(lines, cell(dimStyle.Render(label), 16)+wrapped[0])
		for _, extra := range wrapped[1:] {
			lines = append(lines, cell("", 16)+extra)
		}
	}

	if m.job.loading {
		lines = append(lines, dimStyle.Render("loading…"))
		return lines
	}

	field("status", statusBadge(j.Status))
	field("launch type", rowStyle.Render(j.LaunchType))
	field("started", stamp(j.Started))
	field("finished", stamp(j.Finished))
	// A project update's record carries no inventory at all — it updates a
	// project, not an inventory — and puts the project it updated under
	// summary_fields.project instead.
	if j.Resource() == awx.ResourceProjectUpdates {
		field("project", rowStyle.Render(j.SummaryFields.Project.Name))
	} else {
		field("inventory", rowStyle.Render(j.SummaryFields.Inventory.Name))
	}

	switch {
	case j.IsWorkflow():
		// A workflow job launched from a real template names it here; the
		// implicit workflow a sliced job template creates leaves this empty
		// and carries job_template instead, which awxtui does not show —
		// that job template is the one already named by the sliced run.
		field("workflow template", rowStyle.Render(j.SummaryFields.WorkflowJobTemplate.Name))
		field("limit", rowStyle.Render(j.Limit))
		field("job tags", rowStyle.Render(j.JobTags))
		field("skip tags", rowStyle.Render(j.SkipTags))
	case !j.IsSync():
		field("job template", rowStyle.Render(j.SummaryFields.JobTemplate.Name))
		field("limit", rowStyle.Render(j.Limit))
		field("job tags", rowStyle.Render(j.JobTags))
		field("skip tags", rowStyle.Render(j.SkipTags))
	}
	if !j.IsWorkflow() {
		field("credentials", rowStyle.Render(jobCredentials(j)))
		if ee := j.SummaryFields.ExecutionEnvironment.Name; ee != "" {
			field("execution env", rowStyle.Render(ee))
		}
	}
	if !j.IsSync() && strings.TrimSpace(j.ExtraVars) != "" {
		lines = append(lines, "", titleStyle.Render("extra vars"))
		for _, l := range strings.Split(wrapANSI(rowStyle.Render(j.ExtraVars), width-6), "\n") {
			lines = append(lines, l)
		}
	}

	return lines
}

// jobCredentials names the credentials a job was launched with. A playbook
// job or inventory sync carries a list; a project update's record leaves that
// list empty and puts its one SCM credential under Credential instead.
func jobCredentials(j awx.Job) string {
	if len(j.SummaryFields.Credentials) == 0 {
		if c := j.SummaryFields.Credential.Name; c != "" {
			if k := j.SummaryFields.Credential.Kind; k != "" {
				return c + dimStyle.Render(" ("+k+")")
			}
			return c
		}
		return ""
	}
	names := make([]string, 0, len(j.SummaryFields.Credentials))
	for _, c := range j.SummaryFields.Credentials {
		if c.Kind != "" {
			names = append(names, c.Name+dimStyle.Render(" ("+c.Kind+")"))
			continue
		}
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}
