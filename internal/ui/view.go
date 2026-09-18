package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func wrapANSI(s string, width int) string {
	if width <= 0 {
		return s
	}
	return carryANSIStyle(ansi.Wrap(strings.ReplaceAll(s, "\r\n", "\n"), width, ""))
}

var sgrCode = regexp.MustCompile("\x1b\\[[0-9;]*m")

// carryANSIStyle makes every wrapped line self-contained. ansi.Wrap splits a
// coloured line across several lines but only opens the colour once, at the
// start of the first; a viewport window that starts mid-span (scrolled past
// the opening line) then renders plain, uncoloured text. Reapply the active
// SGR code at the start of each line it still covers.
func carryANSIStyle(s string) string {
	lines := strings.Split(s, "\n")
	active := ""
	for i, line := range lines {
		if active != "" {
			lines[i] = active + line
		}
		for _, code := range sgrCode.FindAllString(line, -1) {
			if code == "\x1b[0m" || code == "\x1b[m" {
				active = ""
			} else {
				active = code
			}
		}
	}
	return strings.Join(lines, "\n")
}

func stripANSI(s string) string { return ansi.Strip(s) }

// columns per tab
var tabColumns = map[tab][]col{
	tabTemplates: {{title: "name", width: 0}, {title: "project", width: 22}, {title: "inventory", width: 20}, {title: "last run", width: 14}, {title: "when", width: 11}},
	// The id column carries the pin marker as well as the number, and AWX job
	// ids run to six digits on an instance of any age.
	tabJobs:        {{title: "id", width: 10}, {title: "name", width: 0}, {title: "status", width: 13}, {title: "elapsed", width: 9}, {title: "started", width: 11}, {title: "by", width: 14}},
	tabInventories: {{title: "name", width: 0}, {title: "organization", width: 22}, {title: "hosts", width: 7}, {title: "groups", width: 7}, {title: "health", width: 14}, {title: "sources", width: 9}},
	tabProjects:    {{title: "name", width: 0}, {title: "scm", width: 10}, {title: "branch", width: 18}, {title: "status", width: 14}, {title: "updated", width: 11}},
	tabSchedules:   {{title: "name", width: 0}, {title: "type", width: 10}, {title: "runs", width: 22}, {title: "next run", width: 14}, {title: "state", width: 9}},
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
	case modeProject:
		b.WriteString(m.pane(m.projectModal()))
	case modeInventory:
		b.WriteString(m.pane(m.inventoryModal()))
	case modeSchedule:
		b.WriteString(m.pane(m.scheduleModal()))
	case modeJob:
		b.WriteString(m.pane(m.jobModal()))
	case modeShow:
		b.WriteString(m.pane(m.showModal()))
	case modePick:
		b.WriteString(m.pane(m.pickerModal()))
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
	switch {
	case m.inflight > 0:
		right = m.spin.View() + metaStyle.Render(" loading")
	case m.user != "":
		right = okStyle.Render("● connected")
	case m.err == nil:
		// The very first request, /api/v2/me/, is in flight before there is a
		// user to name: still busy, so still say so.
		right = m.spin.View() + metaStyle.Render(" connecting")
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
	// What a view is narrowed to has to be on screen: "12" of your own
	// failed runs and "12" of the instance's mean very different things.
	if f := m.show[t]; f.active() {
		label = f.summary() + " · " + label
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
	if !m.loaded[m.active] && m.err == nil && (m.inflight > 0 || m.user == "") {
		b.WriteString("\n  " + m.spin.View() + dimStyle.Render(" fetching "+strings.ToLower(tabNames[m.active])+"…"))
		b.WriteString(strings.Repeat("\n", max(0, m.tableHeight()-1)))
		return b.String()
	}
	if m.searching[m.active] {
		// A pending server search must not show the rows loaded before it
		// was typed: they can look like an empty or stale final answer while
		// the request that would replace them is still in flight.
		b.WriteString("\n  " + m.spin.View() + dimStyle.Render(" searching…"))
		b.WriteString(strings.Repeat("\n", max(0, m.tableHeight()-1)))
		return b.String()
	}
	h := m.tableHeight()
	offset := m.offset[m.active]
	// A page arriving while you scroll deserves a word where the eye already
	// is — at the end of the list. The "⋯" next to the count is too small to
	// notice, which read as a list that had simply stopped.
	more := m.fetching[m.active] && len(rows) > 0
	if more {
		// The spinner and the blank line on either side of it take three
		// lines from the table; re-clamp so the cursor row does not scroll
		// out from under them.
		h = max(1, h-3)
		offset = clampOffset(m.cursor[m.active], offset, h, len(rows))
	}
	table := renderTable(tabColumns[m.active], rows, m.cursor[m.active], offset, m.width-1, h)
	b.WriteString(table)
	lines := countLines(table)
	if more {
		b.WriteString("\n\n  " + m.spin.View() + dimStyle.Render(" loading more "+strings.ToLower(tabNames[m.active])+"…"))
		// The blank line below it comes out of the padding, which always has
		// at least one line to give: the table is three rows shorter.
		lines += 2
	}
	// pad to a stable height so the footer does not jump around
	b.WriteString(strings.Repeat("\n", max(0, m.tableHeight()-lines+1)))
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
	if m.hostLoading {
		b.WriteString("\n  " + m.spin.View() + dimStyle.Render(" fetching hosts…"))
		b.WriteString(strings.Repeat("\n", max(0, h-1)))
		return b.String()
	}
	table := renderTable(hostColumns, m.hostRows, m.hostCursor, m.hostOffset, m.width-1, h)
	b.WriteString(table)
	b.WriteString(strings.Repeat("\n", max(0, h-countLines(table)+1)))
	return b.String()
}

// pane centres content horizontally in the body area. Vertically, a modal
// box shorter than the body grows to fill it (growBox) so any leftover space
// stays inside its border, rather than showing up as a gap between the box
// and the rule and legend that follow — the same total height as listBody
// and outputView fill, just distributed inside the border instead of outside
// it.
func (m Model) pane(content string) string {
	// View adds three rows before the pane (header, tabs and rule) and two
	// after it (closing rule and status bar).  The table budget is height-7,
	// so the pane needs two rows more to fill the remaining height exactly.
	// One row more leaves a terminal row below the legend in every modal.
	h := m.tableHeight() + 2
	return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Top, clip(growBox(content, h), h))
}

// growBox extends a lipgloss-bordered box to height h by repeating the blank
// row lipgloss's own vertical padding inserts just inside the top border —
// it already carries the right width and border colouring — before the
// box's closing border line.
func growBox(content string, h int) string {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || len(lines) >= h {
		return content
	}
	blank := lines[1]
	last := lines[len(lines)-1]
	body := lines[:len(lines)-1]
	for len(body)+1 < h {
		body = append(body, blank)
	}
	return strings.Join(append(body, last), "\n")
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

	if f.submitting {
		body := "\n\n  " + m.spin.View() + dimStyle.Render(" submitting…")
		return modalStyle.Width(inner).Render(head.String() + body + "\n")
	}

	var body string
	if f.canStartImmediately() {
		body = "\n\n" + rowStyle.Render("This template needs no input.") + "\n"
	} else {
		body = "\n\n" + m.formFields(inner)
	}

	foot := ""
	if f.problem != "" {
		foot = "\n\n" + errStyle.Render("✗ "+oneLine(f.problem))
	}
	return modalStyle.Width(inner).Render(head.String() + body + foot)
}

// formKeys is the launch form's legend, which depends on the focused
// field's kind — a multi-choice field takes space and enter differently
// than a plain one. It is shown in the global status bar, the one place a
// legend appears for every view.
func (m Model) formKeys() []legend {
	f := &m.form
	pairs := [][2]string{{"enter", "launch"}, {"↑↓", "field"}}
	if len(f.fields) > 0 {
		switch f.fields[f.cursor].kind {
		case fChoice:
			pairs = append(pairs, [2]string{"←→", "choose"})
		case fMultiChoice:
			pairs = [][2]string{{"ctrl+s", "launch"}, {"↑↓", "field"},
				{"←→", "move"}, {"space", "toggle"}, {"enter", "list"}}
		case fTextarea:
			pairs = [][2]string{{"ctrl+s", "launch"}, {"↑↓", "field"}, {"enter", "newline"}}
		}
	}
	pairs = append(pairs, [2]string{"esc", "cancel"})
	return plainKeys(pairs)
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
		{"enter", "launch · open job output · project or inventory details · in job details: its output"},
		{"s", "sync: SCM update a project · update an inventory's sources"},
		{"h", "in inventory details: list its hosts"},
		{"p", "pin the highlighted record, or the run whose output or details are open"},
		{"f", "narrow what a list shows · follow job output"},
		{"c", "cancel a running job"},
		{"d", "job launch details (from the jobs list or its output)"},
		{"n / N", "next · previous search hit in output"},
		{"] / [", "next · previous failure in output"},
		{"t / T", "next · previous task in output"},
		{"r", "refresh"},
		{"q", "back / quit"},
	}
	for _, g := range groups {
		b.WriteString(cell(helpKeyStyle.Render(g[0]), 18) + helpDescStyle.Render(g[1]) + "\n")
	}
	return modalStyle.Width(min(m.width-6, 76)).Render(strings.TrimSuffix(b.String(), "\n"))
}

func (m Model) statusView() string {
	var keys []legend
	switch m.mode {
	case modeHelp:
		keys = plainKeys([][2]string{{"esc", "close"}})
	case modeError:
		keys = plainKeys([][2]string{{"r", "retry"}, {"esc", "close"}})
	case modeInstances:
		keys = plainKeys([][2]string{{"↑↓", "choose"}, {"enter", "switch"}, {"esc", "cancel"}})
	case modeHosts:
		keys = plainKeys([][2]string{{"↑↓", "move"}, {"esc", "back"}, {"?", "help"}, {"q", "quit"}})
	case modeProject:
		keys = plainKeys([][2]string{{"↑↓", "scroll"}, {"s", "sync"}, {"r", "reload"}, {"esc", "back"}, {"?", "help"}})
	case modeInventory:
		keys = plainKeys([][2]string{{"↑↓", "scroll"}, {"h", "hosts"}, {"s", "sync all"}, {"r", "reload"}, {"esc", "back"}, {"?", "help"}})
	case modeSchedule:
		toggle := "disable"
		if !m.schedule.schedule.Enabled {
			toggle = "enable"
		}
		keys = plainKeys([][2]string{{"↑↓", "scroll"}, {"t", toggle}, {"esc", "back"}, {"?", "help"}})
	case modeJob:
		pin := m.jobPinLabel()
		keys = append(plainKeys([][2]string{{"↑↓", "scroll"}, {"enter", "output"}}),
			legend{key: "p", desc: pin, on: pin == "unpin"})
		keys = append(keys, plainKeys([][2]string{{"r", "reload"}, {"esc", "back"}, {"?", "help"}})...)
	case modeFilter:
		keys = plainKeys([][2]string{{"type", "to filter"}, {"enter", "keep"}, {"esc", "clear"}})
	case modeShow:
		keys = plainKeys([][2]string{{"↑↓", "choose"}, {"←→", "set"}, {"c", "clear"},
			{"enter", "apply"}, {"esc", "cancel"}})
	case modePick:
		keys = plainKeys([][2]string{{"↑↓", "move"}, {"space", "toggle"}, {"a", "all"},
			{"c", "none"}, {"enter", "done"}})
	case modeLaunch:
		keys = m.formKeys()
	default:
		keys = m.listKeys()
	}
	return m.statusOrKeys(keyLegend(keys), "")
}

func plainKeys(pairs [][2]string) []legend {
	items := make([]legend, len(pairs))
	for i, p := range pairs {
		items[i] = legend{key: p[0], desc: p[1]}
	}
	return items
}

// listKeys is the legend of a list view. Two of its entries describe state
// rather than an action always available: p reads "unpin" on a pinned row,
// and f is how a list came to be as short as it is. Both are marked when
// they are in force, so the legend says which view you are looking at.
func (m Model) listKeys() []legend {
	action := "launch"
	switch m.active {
	case tabJobs:
		action = "output"
	case tabInventories, tabProjects, tabSchedules:
		action = "details"
	}
	keys := []legend{{key: "↑↓", desc: "move"}, {key: "enter", desc: action}}
	if m.active == tabProjects || m.active == tabInventories {
		keys = append(keys, legend{key: "s", desc: "sync"})
	}
	if m.active == tabJobs {
		keys = append(keys, legend{key: "d", desc: "details"})
	}
	if m.active == tabSchedules {
		toggle := "disable"
		if s, ok := m.selectedSchedule(); ok && !s.Enabled {
			toggle = "enable"
		}
		keys = append(keys, legend{key: "t", desc: toggle})
	}
	pin := m.pinLabel()
	keys = append(keys,
		legend{key: "p", desc: pin, on: pin == "unpin"},
		legend{key: "f", desc: "show", on: m.show[m.active].active()},
		legend{key: "/", desc: "search", on: m.filters[m.active] != ""},
		legend{key: "r", desc: "refresh"})
	if len(m.instances) > 1 {
		keys = append(keys, legend{key: "i", desc: "instance"})
	}
	return append(keys, legend{key: "?", desc: "help"}, legend{key: "q", desc: "quit"})
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

// legend is one entry of the key line. on marks an action already in force,
// which is rendered differently: on a legend of a dozen muted words, the one
// that describes the state you are in has to be findable at a glance.
type legend struct {
	key, desc string
	on        bool
}

func keyLegend(items []legend) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		desc := helpDescStyle.Render(it.desc)
		if it.on {
			desc = helpOnStyle.Render(it.desc)
		}
		parts = append(parts, helpKeyStyle.Render(it.key)+" "+desc)
	}
	return strings.Join(parts, helpDescStyle.Render(" · "))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func countLines(s string) int { return strings.Count(s, "\n") + 1 }
