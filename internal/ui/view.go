package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func wrapANSI(s string, width int) string {
	if width <= 0 {
		return s
	}
	return ansi.Wrap(strings.ReplaceAll(s, "\r\n", "\n"), width, "")
}

func stripANSI(s string) string { return ansi.Strip(s) }

// columns per tab
var tabColumns = map[tab][]col{
	tabTemplates:   {{title: "name", width: 0}, {title: "project", width: 22}, {title: "inventory", width: 20}, {title: "last run", width: 14}, {title: "when", width: 11}},
	tabJobs:        {{title: "id", width: 7}, {title: "name", width: 0}, {title: "status", width: 13}, {title: "elapsed", width: 9}, {title: "started", width: 11}, {title: "by", width: 14}},
	tabInventories: {{title: "name", width: 0}, {title: "organization", width: 22}, {title: "hosts", width: 7}, {title: "groups", width: 7}, {title: "health", width: 14}},
	tabProjects:    {{title: "name", width: 0}, {title: "scm", width: 10}, {title: "branch", width: 18}, {title: "status", width: 14}, {title: "updated", width: 11}},
}

var hostColumns = []col{{title: "host", width: 0}, {title: "state", width: 10}, {title: "description", width: 32}}

func (m Model) View() string {
	if !m.ready {
		return "\n  " + m.spin.View() + " connecting to AWX…\n"
	}
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	b.WriteString(m.tabsView())
	b.WriteString("\n")
	b.WriteString(m.rule())
	b.WriteString("\n")

	switch m.mode {
	case modeOutput:
		return m.outputView()
	case modeLaunch:
		b.WriteString(m.pane(m.launchModal()))
	case modeHelp:
		b.WriteString(m.pane(m.helpModal()))
	case modeHosts:
		b.WriteString(m.hostsBody())
	default:
		b.WriteString(m.listBody())
	}
	b.WriteString("\n")
	b.WriteString(m.rule())
	b.WriteString("\n")
	b.WriteString(m.statusView())
	return b.String()
}

func (m Model) rule() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	return ruleStyle.Render(strings.Repeat("─", w))
}

func (m Model) headerView() string {
	left := titleStyle.Render("awxtui")
	host := m.client.BaseURL()
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	who := host
	if m.user != "" {
		who = m.user + "@" + host
	}
	left += "  " + metaStyle.Render(who)

	right := ""
	if m.inflight > 0 {
		right = m.spin.View() + metaStyle.Render(" loading")
	} else if m.user != "" {
		right = okStyle.Render("● connected")
	}
	return m.spread(left, right)
}

// spread puts left and right on one line, padded to the terminal width.
func (m Model) spread(left, right string) string {
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return cell(left, max(0, m.width-lipgloss.Width(right))) + right
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) tabsView() string {
	parts := make([]string, 0, tabCount)
	for t := tab(0); t < tabCount; t++ {
		label := fmt.Sprintf("%d %s", t+1, tabNames[t])
		if t == m.active && m.mode != modeHosts {
			parts = append(parts, tabActiveStyle.Render(label))
		} else {
			parts = append(parts, tabStyle.Render(label))
		}
	}
	left := strings.Join(parts, "")
	right := ""
	if m.mode == modeHosts {
		right = metaStyle.Render("inventory ▸ ") + rowStyle.Render(m.hostTitle)
	}
	return m.spread(left, right)
}

// filterLine shows the active filter or a hint, always occupying one line.
func (m Model) filterLine(shown, total int) string {
	var left string
	switch {
	case m.mode == modeFilter:
		left = m.filterInput.View()
	case m.filters[m.active] != "":
		left = helpKeyStyle.Render("search ") + rowStyle.Render(m.filters[m.active]) + dimStyle.Render("  (esc to clear)")
	default:
		left = dimStyle.Render("press / to search")
	}
	right := dimStyle.Render(fmt.Sprintf("%d/%d", shown, total))
	return m.spread(left, right)
}

func (m Model) listBody() string {
	rows := m.visible(m.active)
	var b strings.Builder
	b.WriteString(m.filterLine(len(rows), len(m.rows[m.active])))
	b.WriteString("\n")
	if !m.loaded[m.active] && m.inflight > 0 {
		b.WriteString("\n  " + m.spin.View() + dimStyle.Render(" fetching "+strings.ToLower(tabNames[m.active])+"…"))
		b.WriteString(strings.Repeat("\n", max(0, m.tableHeight()-1)))
		return b.String()
	}
	h := m.tableHeight()
	table := renderTable(tabColumns[m.active], rows, m.cursor[m.active], m.offset[m.active], m.width-1, h)
	b.WriteString(table)
	// pad to a stable height so the footer does not jump around
	b.WriteString(strings.Repeat("\n", max(0, h-countLines(table)+1)))
	return b.String()
}

func (m Model) hostsBody() string {
	var b strings.Builder
	b.WriteString(m.spread(dimStyle.Render("esc to go back"), dimStyle.Render(fmt.Sprintf("%d hosts", len(m.hostRows)))))
	b.WriteString("\n")
	h := m.tableHeight()
	table := renderTable(hostColumns, m.hostRows, m.hostCursor, m.hostOffset, m.width-1, h)
	b.WriteString(table)
	b.WriteString(strings.Repeat("\n", max(0, h-countLines(table)+1)))
	return b.String()
}

// pane centres content in the body area.
func (m Model) pane(content string) string {
	h := m.tableHeight() + 1
	return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Center, clip(content, h))
}

// clip drops lines that would not fit in h, keeping the view inside the screen.
func clip(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= h {
		return s
	}
	return strings.Join(lines[:max(h, 1)], "\n")
}

func (m Model) launchModal() string {
	t := m.launchTarget
	if t == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Launch job template"))
	b.WriteString("\n\n")
	b.WriteString(rowStyle.Render(t.Name))
	b.WriteString("\n")
	meta := []string{fmt.Sprintf("#%d", t.ID)}
	if t.SummaryFields.Project.Name != "" {
		meta = append(meta, "project "+t.SummaryFields.Project.Name)
	}
	if t.SummaryFields.Inventory.Name != "" {
		meta = append(meta, "inventory "+t.SummaryFields.Inventory.Name)
	}
	if t.Playbook != "" {
		meta = append(meta, t.Playbook)
	}
	b.WriteString(metaStyle.Render(strings.Join(meta, "  ·  ")))
	b.WriteString("\n\n")
	b.WriteString(m.varsInput.View())
	b.WriteString("\n\n")
	b.WriteString(keyHelp([][2]string{{"enter", "launch"}, {"esc", "cancel"}}))
	return modalStyle.Width(min(m.width-6, 72)).Render(b.String())
}

func (m Model) helpModal() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Keys"))
	b.WriteString("\n\n")
	groups := [][2]string{
		{"1-4 / tab", "switch view"},
		{"↑↓ j k", "move  ·  ctrl+d ctrl+u half page  ·  g G top bottom"},
		{"/", "search (esc clears)"},
		{"enter", "launch · open job output · list inventory hosts"},
		{"c", "cancel a running job"},
		{"f", "follow job output"},
		{"r", "refresh"},
		{"q", "back / quit"},
	}
	for _, g := range groups {
		b.WriteString(cell(helpKeyStyle.Render(g[0]), 18) + helpDescStyle.Render(g[1]) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("press any key to close"))
	return modalStyle.Width(min(m.width-6, 76)).Render(b.String())
}

func (m Model) outputView() string {
	j := m.outputJob
	head := titleStyle.Render(fmt.Sprintf("#%d", j.ID)) + "  " + rowStyle.Render(j.Name)
	right := statusBadge(j.Status) + metaStyle.Render("  "+duration(j.Elapsed))
	if j.IsRunning() {
		if m.follow {
			right += okStyle.Render("  ⟳ follow")
		} else {
			right += dimStyle.Render("  ⏸ paused")
		}
	}
	var b strings.Builder
	b.WriteString(m.spread(head, right))
	b.WriteString("\n")
	b.WriteString(m.rule())
	b.WriteString("\n")
	if strings.TrimSpace(m.outputText) == "" {
		body := dimStyle.Render("  waiting for output…")
		b.WriteString(body)
		b.WriteString(strings.Repeat("\n", max(0, m.outputHeight()-1)))
	} else {
		b.WriteString(m.vp.View())
	}
	b.WriteString("\n")
	b.WriteString(m.rule())
	b.WriteString("\n")
	keys := [][2]string{{"↑↓", "scroll"}, {"f", "follow"}, {"g/G", "top/bottom"}, {"r", "reload"}}
	if j.IsRunning() {
		keys = append(keys, [2]string{"c", "cancel job"})
	}
	keys = append(keys, [2]string{"esc", "back"})
	left := keyHelp(keys)
	right = dimStyle.Render(fmt.Sprintf("%3.0f%%", m.vp.ScrollPercent()*100))
	b.WriteString(m.statusOrKeys(left, right))
	return b.String()
}

func (m Model) statusView() string {
	var keys [][2]string
	switch m.mode {
	case modeHosts:
		keys = [][2]string{{"↑↓", "move"}, {"esc", "back"}, {"?", "help"}, {"q", "quit"}}
	case modeFilter:
		keys = [][2]string{{"type", "to filter"}, {"enter", "keep"}, {"esc", "clear"}}
	default:
		action := "launch"
		switch m.active {
		case tabJobs:
			action = "output"
		case tabInventories:
			action = "hosts"
		case tabProjects:
			action = "details"
		}
		keys = [][2]string{{"↑↓", "move"}, {"enter", action}, {"/", "search"}, {"r", "refresh"}, {"?", "help"}, {"q", "quit"}}
	}
	return m.statusOrKeys(keyHelp(keys), "")
}

// statusOrKeys shows an error or notice when there is one, otherwise the keys.
func (m Model) statusOrKeys(keys, right string) string {
	switch {
	case m.err != nil:
		msg := errStyle.Render("✗ " + oneLine(m.err.Error()))
		return m.spread(cell(msg, max(0, m.width-lipgloss.Width(right)-1)), right)
	case m.notice != "":
		msg := okStyle.Render("✓ " + oneLine(m.notice))
		return m.spread(cell(msg, max(0, m.width-lipgloss.Width(right)-1)), right)
	}
	return m.spread(keys, right)
}

func keyHelp(pairs [][2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, helpKeyStyle.Render(p[0])+" "+helpDescStyle.Render(p[1]))
	}
	return strings.Join(parts, helpDescStyle.Render(" · "))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func countLines(s string) int { return strings.Count(s, "\n") + 1 }
