package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// maxProjectPlaybooks bounds how many playbook names the details view lists.
// A monorepo project can report hundreds; the rest are summarised in a line.
const maxProjectPlaybooks = 200

// projectDetail is the state of the project details view. The project record
// comes straight from the list — AWX's project list returns the full record —
// so only the playbook names need fetching.
type projectDetail struct {
	project   awx.Project
	playbooks []string
	loading   bool
	offset    int
}

// openProject shows the details of the selected project and asks AWX for its
// playbooks.
func (m *Model) openProject(p awx.Project) tea.Cmd {
	m.err, m.notice = nil, ""
	m.mode = modeProject
	m.project = projectDetail{project: p, loading: true}
	m.inflight++
	return m.fetchPlaybooks(p.ID)
}

// selectedProject resolves the highlighted row back to its project record.
func (m *Model) selectedProject() (awx.Project, bool) {
	r, ok := m.selected()
	if !ok {
		return awx.Project{}, false
	}
	for _, p := range m.projects {
		if p.ID == r.id {
			return p, true
		}
	}
	return awx.Project{}, false
}

func (m Model) handleProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc", "enter":
		m.mode = modeList
		m.project = projectDetail{}
		return m, nil
	case "up", "k":
		m.project.offset = m.clampProjectOffset(m.project.offset - 1)
		return m, nil
	case "down", "j":
		m.project.offset = m.clampProjectOffset(m.project.offset + 1)
		return m, nil
	case "pgup", "ctrl+u":
		m.project.offset = m.clampProjectOffset(m.project.offset - m.projectWindow()/2)
		return m, nil
	case "pgdown", "ctrl+d":
		m.project.offset = m.clampProjectOffset(m.project.offset + m.projectWindow()/2)
		return m, nil
	case "home", "g":
		m.project.offset = 0
		return m, nil
	case "end", "G":
		m.project.offset = m.clampProjectOffset(1 << 30)
		return m, nil
	case "r":
		m.project.loading = true
		m.inflight++
		return m, m.fetchPlaybooks(m.project.project.ID)
	case "s":
		p := m.project.project
		cmd := m.startSync("project "+p.Name, m.syncProject(p))
		if cmd == nil {
			return m, nil
		}
		// The update's output replaces the details, which are about to be
		// out of date anyway.
		m.mode = modeList
		m.project = projectDetail{}
		return m, cmd
	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
}

// projectWindow is how many body lines the modal can show at once. The modal
// spends lines on its border, padding, title and key legend.
func (m Model) projectWindow() int {
	return max(m.tableHeight()-7, 3)
}

func (m Model) clampProjectOffset(off int) int {
	return clamp(off, 0, len(m.projectBody(m.projectWidth()))-m.projectWindow())
}

func (m Model) projectWidth() int {
	return min(m.width-8, 84)
}

// projectModal renders the details of one project.
func (m Model) projectModal() string {
	p := m.project.project
	width := m.projectWidth()
	lines := m.projectBody(width)
	window := m.projectWindow()
	off := clamp(m.project.offset, 0, max(len(lines)-window, 0))
	end := min(off+window, len(lines))

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Project #%d", p.ID)) + "  " + rowStyle.Bold(true).Render(p.Name))
	if len(lines) > window {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  (%d–%d of %d lines)", off+1, end, len(lines))))
	}
	b.WriteString("\n\n")
	b.WriteString(strings.Join(lines[off:end], "\n"))
	b.WriteString("\n\n")
	keys := [][2]string{{"esc", "close"}, {"s", "sync"}, {"r", "reload playbooks"}}
	if len(lines) > window {
		keys = append([][2]string{{"↑↓", "scroll"}}, keys...)
	}
	b.WriteString(keyHelp(keys))
	return modalStyle.Width(width).Render(b.String())
}

// projectBody is the scrollable content of the modal, one string per line.
func (m Model) projectBody(width int) []string {
	p := m.project.project
	// The label column, plus the modal's border and padding.
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

	if d := strings.TrimSpace(p.Description); d != "" {
		for _, l := range strings.Split(wrapANSI(metaStyle.Render(d), width-6), "\n") {
			lines = append(lines, l)
		}
		lines = append(lines, "")
	}

	field("status", statusBadge(p.Status))
	field("last updated", stamp(p.LastUpdated))
	field("organization", rowStyle.Render(p.SummaryFields.Organization.Name))
	field("source", rowStyle.Render(p.SCMTypeLabel()))
	field("url", rowStyle.Render(p.SCMURL))
	field("branch", rowStyle.Render(p.SCMBranch))
	if p.SCMRefspec != "" {
		field("refspec", rowStyle.Render(p.SCMRefspec))
	}
	field("revision", rowStyle.Render(p.SCMRevision))
	if c := p.SummaryFields.Credential; c.Name != "" {
		field("credential", rowStyle.Render(c.Name)+dimStyle.Render("  "+c.Kind))
	}
	field("local path", rowStyle.Render(p.LocalPath))
	field("options", rowStyle.Render(projectOptions(p)))
	field("cache timeout", rowStyle.Render(seconds(p.SCMUpdateCacheTimeout)))
	field("job timeout", rowStyle.Render(seconds(p.Timeout)))
	field("created", stamp(p.Created))

	lines = append(lines, "")
	switch {
	case m.project.loading:
		lines = append(lines, dimStyle.Render("playbooks  loading…"))
	case len(m.project.playbooks) == 0:
		lines = append(lines, dimStyle.Render("playbooks  none reported"))
	default:
		names := m.project.playbooks
		over := 0
		if len(names) > maxProjectPlaybooks {
			over = len(names) - maxProjectPlaybooks
			names = names[:maxProjectPlaybooks]
		}
		lines = append(lines, headerStyle.Render(fmt.Sprintf("playbooks (%d)", len(m.project.playbooks))))
		for _, n := range names {
			lines = append(lines, "  "+rowStyle.Render(truncateTo(n, width-8)))
		}
		if over > 0 {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("  … %d more not shown", over)))
		}
	}
	return lines
}

// projectOptions lists the update behaviour flags AWX has set, so an empty
// line means "none of them" rather than "not loaded".
func projectOptions(p awx.Project) string {
	var on []string
	if p.SCMUpdateOnLaunch {
		on = append(on, "update on launch")
	}
	if p.SCMClean {
		on = append(on, "clean")
	}
	if p.SCMDeleteOnUpdate {
		on = append(on, "delete on update")
	}
	if p.SCMTrackSubmodules {
		on = append(on, "track submodules")
	}
	if p.AllowOverride {
		on = append(on, "allow branch override")
	}
	if len(on) == 0 {
		return ""
	}
	return strings.Join(on, ", ")
}

// stamp renders an optional timestamp as both a relative and an absolute
// time: "how long ago" is what you want at a glance, the clock time is what
// you need when correlating with a change elsewhere. Past a month ago() gives
// a date itself, and repeating it twice on one line reads as a bug.
func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	local := t.Local()
	rel := ago(*t)
	if rel == t.Format("2006-01-02") {
		return rowStyle.Render(local.Format("2006-01-02 15:04"))
	}
	return rowStyle.Render(rel) + dimStyle.Render("  "+local.Format("2006-01-02 15:04"))
}

func seconds(n int) string {
	if n == 0 {
		return "none"
	}
	return fmt.Sprintf("%ds", n)
}
