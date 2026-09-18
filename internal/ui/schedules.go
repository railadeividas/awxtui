package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// scheduleDetail is the state of the schedule details view. The schedule
// record comes straight from the list — AWX's schedule list returns the full
// record — so opening it needs no follow-up fetch.
type scheduleDetail struct {
	schedule awx.Schedule
	offset   int
}

// openSchedule shows the details of the selected schedule.
func (m *Model) openSchedule(s awx.Schedule) tea.Cmd {
	m.err, m.notice = nil, ""
	m.mode = modeSchedule
	m.schedule = scheduleDetail{schedule: s}
	return nil
}

// selectedSchedule resolves the highlighted row back to its schedule record.
func (m *Model) selectedSchedule() (awx.Schedule, bool) {
	r, ok := m.selected()
	if !ok {
		return awx.Schedule{}, false
	}
	for _, s := range m.schedules {
		if s.ID == r.id {
			return s, true
		}
	}
	return awx.Schedule{}, false
}

// toggleSelectedScheduleEnabled flips the enabled flag of the highlighted
// schedule on the list. A held-down key must not fire the same PATCH twice
// while the first is still in flight.
func (m *Model) toggleSelectedScheduleEnabled() tea.Cmd {
	s, ok := m.selectedSchedule()
	if !ok {
		return nil
	}
	return m.startScheduleToggle(s)
}

func (m *Model) startScheduleToggle(s awx.Schedule) tea.Cmd {
	if m.client.IsReadOnly() {
		m.err = fmt.Errorf("read-only mode: toggling a schedule is disabled")
		return nil
	}
	if m.toggling {
		return nil
	}
	m.err = nil
	if s.Enabled {
		m.notice = "disabling " + s.Name + "…"
	} else {
		m.notice = "enabling " + s.Name + "…"
	}
	m.toggling = true
	return m.toggleScheduleEnabled(s)
}

func (m Model) handleScheduleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc", "enter":
		m.mode = modeList
		m.schedule = scheduleDetail{}
		return m, nil
	case "up", "k":
		m.schedule.offset = m.clampScheduleOffset(m.schedule.offset - 1)
		return m, nil
	case "down", "j":
		m.schedule.offset = m.clampScheduleOffset(m.schedule.offset + 1)
		return m, nil
	case "pgup", "ctrl+u":
		m.schedule.offset = m.clampScheduleOffset(m.schedule.offset - m.scheduleWindow()/2)
		return m, nil
	case "pgdown", "ctrl+d":
		m.schedule.offset = m.clampScheduleOffset(m.schedule.offset + m.scheduleWindow()/2)
		return m, nil
	case "home", "g":
		m.schedule.offset = 0
		return m, nil
	case "end", "G":
		m.schedule.offset = m.clampScheduleOffset(1 << 30)
		return m, nil
	case "t":
		cmd := m.startScheduleToggle(m.schedule.schedule)
		return m, cmd
	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
}

// scheduleWindow is how many body lines the modal can show at once. The modal
// spends lines on its border, padding, title and key legend.
func (m Model) scheduleWindow() int {
	return max(m.tableHeight()-7, 3)
}

func (m Model) clampScheduleOffset(off int) int {
	return clamp(off, 0, len(m.scheduleBody(m.scheduleWidth()))-m.scheduleWindow())
}

func (m Model) scheduleWidth() int {
	return min(m.width-8, 84)
}

// scheduleModal renders the details of one schedule.
func (m Model) scheduleModal() string {
	s := m.schedule.schedule
	width := m.scheduleWidth()
	lines := m.scheduleBody(width)
	window := m.scheduleWindow()
	off := clamp(m.schedule.offset, 0, max(len(lines)-window, 0))
	end := min(off+window, len(lines))

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Schedule #%d", s.ID)) + "  " + rowStyle.Bold(true).Render(s.Name))
	if len(lines) > window {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  (%d–%d of %d lines)", off+1, end, len(lines))))
	}
	b.WriteString("\n\n")
	b.WriteString(strings.Join(lines[off:end], "\n"))
	return modalStyle.Width(width).Render(b.String())
}

// scheduleBody is the scrollable content of the modal, one string per line.
func (m Model) scheduleBody(width int) []string {
	s := m.schedule.schedule
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

	if d := strings.TrimSpace(s.Description); d != "" {
		for _, l := range strings.Split(wrapANSI(metaStyle.Render(d), width-6), "\n") {
			lines = append(lines, l)
		}
		lines = append(lines, "")
	}

	enabled := okStyle.Render("enabled")
	if !s.Enabled {
		enabled = dimStyle.Render("disabled")
	}
	field("state", enabled)
	field("runs", rowStyle.Render(scheduleKindLabel(s.SummaryFields.UnifiedJobTemplate.UnifiedJobType))+
		dimStyle.Render("  "+s.SummaryFields.UnifiedJobTemplate.Name))
	field("next run", dueStamp(s.NextRun))
	field("timezone", rowStyle.Render(s.Timezone))
	field("rule", rowStyle.Render(s.RRule))
	field("created", stamp(s.Created))
	field("modified", stamp(s.Modified))

	return lines
}
