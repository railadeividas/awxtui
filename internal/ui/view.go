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
	case modeError:
		b.WriteString(m.pane(m.errorModal()))
	case modeInstances:
		b.WriteString(m.pane(m.instancesModal()))
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
	if m.instance != "" {
		chip := m.instance
		if len(m.instances) > 1 {
			chip += " ▾"
		}
		left += "  " + tabActiveStyle.Render(chip)
	}
	host := m.client.BaseURL()
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	who := host
	if m.user != "" {
		who = m.user + "@" + host
	}
	left += "  " + metaStyle.Render(who)

	if m.client.IsReadOnly() {
		left += "  " + warnStyle.Render("read-only")
	}

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

// countLabel summarises how much of a list is loaded, and how much of it the
// current filter matches.
func (m Model) countLabel(t tab) string {
	loaded, total, shown := len(m.rows[t]), m.count[t], len(m.visible(t))
	var label string
	switch {
	case m.searching[t]:
		return "searching…"
	case m.serverQuery[t] != "" && total > loaded:
		label = fmt.Sprintf("%d of %d matching", loaded, total)
	case m.serverQuery[t] != "":
		label = fmt.Sprintf("%d matching", loaded)
	case m.filters[t] != "":
		label = fmt.Sprintf("%d matched · %d", shown, loaded)
	case total > loaded:
		label = fmt.Sprintf("%d of %d", loaded, total)
	default:
		label = fmt.Sprintf("%d", loaded)
	}
	if m.fetching[t] {
		label += " ⋯"
	} else if m.next[t] != "" && m.pages[t] >= maxPages {
		label += " (page limit)"
	}
	return label
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
	right := dimStyle.Render(m.countLabel(m.active))
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
	hosts := fmt.Sprintf("%d hosts", len(m.hostRows))
	if m.hostCount > len(m.hostRows) {
		hosts = fmt.Sprintf("%d of %d hosts", len(m.hostRows), m.hostCount)
	}
	b.WriteString(m.spread(dimStyle.Render("esc to go back"), dimStyle.Render(hosts)))
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

// launchModal renders the launch form: prompted values, survey questions and
// credential passwords, scrolled so the focused field stays visible.
func (m Model) launchModal() string {
	f := &m.form
	inner := min(m.width-8, 78)

	var head strings.Builder
	head.WriteString(titleStyle.Render("Launch") + "  " + rowStyle.Render(f.template.Name))
	head.WriteString("\n")
	meta := []string{fmt.Sprintf("#%d", f.template.ID)}
	if p := f.template.SummaryFields.Project.Name; p != "" {
		meta = append(meta, "project "+p)
	}
	if inv := f.config.Defaults.Inventory.Name; inv != "" {
		meta = append(meta, "inventory "+inv)
	}
	if f.template.Playbook != "" {
		meta = append(meta, f.template.Playbook)
	}
	head.WriteString(metaStyle.Render(strings.Join(meta, "  ·  ")))

	if m.client.IsReadOnly() {
		head.WriteString("\n" + warnStyle.Render("read-only mode: this form cannot be submitted"))
	}

	var body string
	if f.canStartImmediately() {
		body = "\n\n" + rowStyle.Render("This template needs no input.") + "\n"
	} else {
		body = "\n\n" + m.formFields(inner)
	}

	keys := [][2]string{{"enter", "launch"}, {"↑↓", "field"}}
	if len(f.fields) > 0 {
		switch f.fields[f.cursor].kind {
		case fChoice:
			keys = append(keys, [2]string{"←→", "choose"})
		case fMultiChoice:
			keys = append(keys, [2]string{"←→", "move"}, [2]string{"space", "toggle"})
		case fTextarea:
			keys = [][2]string{{"ctrl+s", "launch"}, {"↑↓", "field"}, {"enter", "newline"}}
		}
	}
	keys = append(keys, [2]string{"esc", "cancel"})

	foot := "\n\n" + keyHelp(keys)
	if f.problem != "" {
		foot = "\n\n" + errStyle.Render("✗ "+oneLine(f.problem)) + "\n" + keyHelp(keys)
	}
	return modalStyle.Width(inner).Render(head.String() + body + foot)
}

// formFields renders the field list, windowed around the focused field.
func (m Model) formFields(inner int) string {
	f := &m.form
	labelW := 0
	for i := range f.fields {
		if w := lipgloss.Width(fieldLabel(&f.fields[i])); w > labelW {
			labelW = w
		}
	}
	labelW = min(labelW, max(inner/3, 12))

	blocks := make([]string, len(f.fields))
	for i := range f.fields {
		blocks[i] = m.renderField(&f.fields[i], i == f.cursor, labelW, inner)
	}

	// Budget: the modal body minus header, footer and border.
	budget := max(m.tableHeight()-8, 4)
	first, last, used := f.cursor, f.cursor, countLines(blocks[f.cursor])
	for first > 0 || last < len(blocks)-1 {
		grew := false
		if first > 0 && used+countLines(blocks[first-1])+sectionLines(f, first-1) <= budget {
			first--
			used += countLines(blocks[first]) + sectionLines(f, first)
			grew = true
		}
		if last < len(blocks)-1 && used+countLines(blocks[last+1])+sectionLines(f, last+1) <= budget {
			last++
			used += countLines(blocks[last]) + sectionLines(f, last)
			grew = true
		}
		if !grew {
			break
		}
	}

	var b strings.Builder
	if first > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more", first)) + "\n")
	}
	for i := first; i <= last; i++ {
		if sectionLines(f, i) > 0 {
			heading := string(f.fields[i].section)
			if f.fields[i].section == secSurvey && strings.TrimSpace(f.survey.Name) != "" {
				heading = f.survey.Name
			}
			b.WriteString(headerStyle.Render(strings.ToUpper(heading)) + "\n")
		}
		b.WriteString(blocks[i])
		if i < last {
			b.WriteString("\n")
		}
	}
	if last < len(blocks)-1 {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(blocks)-1-last)))
	}
	return b.String()
}

// sectionLines reports whether field i starts a new section heading.
func sectionLines(f *form, i int) int {
	if i == 0 || f.fields[i].section != f.fields[i-1].section {
		return 1
	}
	return 0
}

func fieldLabel(fl *formField) string {
	label := fl.label
	if fl.required {
		label = "*" + label
	}
	return label
}

func (m Model) renderField(fl *formField, focused bool, labelW, inner int) string {
	prefix, label := " ", dimStyle.Render(cell(fieldLabel(fl), labelW))
	if focused {
		prefix = helpKeyStyle.Render("▌")
		label = rowStyle.Bold(true).Render(cell(fieldLabel(fl), labelW))
	}

	var value string
	switch fl.kind {
	case fChoice:
		choice := "—"
		if fl.idx < len(fl.choices) {
			choice = fl.choices[fl.idx]
		}
		marker := dimStyle
		if focused {
			marker = helpKeyStyle
		}
		value = marker.Render("‹ ") + rowStyle.Render(choice) + marker.Render(" ›")

	case fMultiChoice:
		// The modal is inner wide including its 2-column padding either side,
		// and the line already spent a marker, the label and two gaps.
		value = "  " + multiChoiceWindow(fl, focused, max(inner-4-labelW-5, 10))

	case fTextarea:
		if focused {
			lines := strings.Split(fl.area.View(), "\n")
			for i, l := range lines {
				lines[i] = "    " + l
			}
			return prefix + label + "\n" + strings.Join(lines, "\n")
		}
		v := strings.TrimSpace(fl.area.Value())
		switch {
		case v == "":
			value = dimStyle.Render("  (empty)")
		case strings.Contains(v, "\n"):
			value = rowStyle.Render("  "+oneLine(v)) + dimStyle.Render(" …")
		default:
			value = rowStyle.Render("  " + v)
		}

	default:
		if focused {
			value = "  " + fl.input.View()
		} else if v := fl.input.Value(); v != "" {
			if fl.kind == fPassword {
				v = strings.Repeat("•", min(len(v), 12))
			}
			value = rowStyle.Render("  " + v)
		} else {
			value = dimStyle.Render("  " + firstNonEmpty(fl.input.Placeholder, "(empty)"))
		}
	}

	line := prefix + label + "  " + value
	if focused && fl.help != "" {
		line += "\n" + cell("", labelW+2) + dimStyle.Render(oneLine(fl.help))
	}
	return line
}

// errorModal shows the whole error, which the one-line status bar has to cut.
func (m Model) errorModal() string {
	if m.err == nil {
		return ""
	}
	width := min(m.width-8, 80)
	var b strings.Builder
	b.WriteString(errStyle.Bold(true).Render("Error"))
	b.WriteString("\n\n")
	lines := strings.Split(wrapANSI(m.err.Error(), width-6), "\n")
	if cap := max(m.tableHeight()-8, 3); len(lines) > cap {
		lines = append(lines[:cap], dimStyle.Render("… message truncated"))
	}
	b.WriteString(rowStyle.Render(strings.Join(lines, "\n")))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("press any key to close · r retries the current view"))
	return modalStyle.BorderForeground(danger).Width(width).Render(b.String())
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
		{"n / N", "next · previous search hit in output"},
		{"] / [", "next · previous failure in output"},
		{"t / T", "next · previous task in output"},
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
		keys = [][2]string{{"↑↓", "move"}, {"enter", action}, {"/", "search"}, {"r", "refresh"}}
		if len(m.instances) > 1 {
			keys = append(keys, [2]string{"i", "instance"})
		}
		keys = append(keys, [2]string{"?", "help"}, [2]string{"q", "quit"})
	}
	return m.statusOrKeys(keyHelp(keys), "")
}

// statusOrKeys keeps the key legend on screen and puts an error or notice to
// its right. A message is transient; the keys are how you leave the screen, so
// the keys hold their place and the message takes whatever room is left. Only
// when that room is too small to say anything useful does the message take the
// line — an unreadable error is worse than a missing legend.
func (m Model) statusOrKeys(keys, right string) string {
	var msg, hint string
	switch {
	case m.err != nil:
		msg = errStyle.Render("✗ " + oneLine(m.err.Error()))
		hint = dimStyle.Render("e for details")
	case m.notice != "":
		msg = okStyle.Render("✓ " + oneLine(m.notice))
	default:
		return m.spread(keys, right)
	}

	// Enough for a short notice or the head of an error; below that the
	// message would say nothing, so it takes the line instead.
	const minMsg = 16
	reserved := lipgloss.Width(keys) + lipgloss.Width(right) + lipgloss.Width(hint) + 6
	if room := m.width - reserved; room >= minMsg {
		tail := truncateTo(msg, room)
		if hint != "" {
			tail += "  " + hint
		}
		if right != "" {
			tail += "  " + right
		}
		return m.spread(keys, tail)
	}
	if hint != "" {
		right = dimStyle.Render("e for details  ") + right
	}
	return m.spread(cell(msg, max(0, m.width-lipgloss.Width(right)-1)), right)
}

// multiChoiceWindow renders a multi-select inside width columns. A focused
// field shows checkboxes in a window around the highlighted entry, with a
// count of what is hidden on either side; an unfocused one shows only what is
// selected. The catalogue prompts run to dozens of entries — 19 instance
// groups and 52 credentials on the instance this was built against — so the
// whole list can never be rendered inline.
// hiddenMarkerWidth is the room kept for "‹12 " and " 41›" together.
const hiddenMarkerWidth = 10

func multiChoiceWindow(fl *formField, focused bool, width int) string {
	if len(fl.choices) == 0 {
		return dimStyle.Render("(none available)")
	}
	if !focused {
		sel := fl.selections()
		if len(sel) == 0 {
			return dimStyle.Render("(none selected)")
		}
		summary := strings.Join(sel, ", ")
		if len(sel) < len(fl.choices) {
			summary = fmt.Sprintf("%d of %d: %s", len(sel), len(fl.choices), summary)
		}
		return rowStyle.Render(truncateTo(summary, width))
	}

	item := func(i int) string {
		box := "[ ]"
		if i < len(fl.chosen) && fl.chosen[i] {
			box = "[x]"
		}
		return box + " " + fl.choices[i]
	}
	// Grow outwards from the highlighted entry for as long as the next one
	// fits. Widening first and then last keeps the window symmetric.
	fit := func(budget int) (int, int) {
		first, last := fl.idx, fl.idx
		used := lipgloss.Width(item(fl.idx))
		for first > 0 || last < len(fl.choices)-1 {
			grew := false
			if last < len(fl.choices)-1 {
				if w := used + 2 + lipgloss.Width(item(last+1)); w <= budget {
					last, used, grew = last+1, w, true
				}
			}
			if first > 0 {
				if w := used + 2 + lipgloss.Width(item(first-1)); w <= budget {
					first, used, grew = first-1, w, true
				}
			}
			if !grew {
				return first, last
			}
		}
		return first, last
	}
	first, last := fit(width)
	if first > 0 || last < len(fl.choices)-1 {
		// Some entries are hidden, so the "‹12 … 41›" counts need room of
		// their own; without it they would be truncated straight back off.
		first, last = fit(width - hiddenMarkerWidth)
	}

	parts := make([]string, 0, last-first+1)
	for i := first; i <= last; i++ {
		s := item(i)
		switch {
		case i == fl.idx:
			s = helpKeyStyle.Render(s)
		case i < len(fl.chosen) && fl.chosen[i]:
			s = rowStyle.Render(s)
		default:
			s = dimStyle.Render(s)
		}
		parts = append(parts, s)
	}
	out := strings.Join(parts, "  ")
	if first > 0 {
		out = dimStyle.Render(fmt.Sprintf("‹%d ", first)) + out
	}
	if last < len(fl.choices)-1 {
		out += dimStyle.Render(fmt.Sprintf(" %d›", len(fl.choices)-1-last))
	}
	// A single entry can still be wider than the window on a narrow terminal.
	return truncateTo(out, width)
}

// truncateTo shortens s to w display cells, keeping its ANSI styling intact.
func truncateTo(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s
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
