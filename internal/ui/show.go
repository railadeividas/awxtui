package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// `/` asks AWX for text that matches. `f` answers a different question:
// which records belong on screen at all. The two compose — a search inside a
// narrowed view searches the narrowed view — and both are sent to AWX rather
// than applied to the rows that happen to be loaded, because every list here
// is paged and the interesting record is usually not on page one.

// showFilter is what a tab is currently showing. The zero value shows
// everything, so a tab that has never been narrowed behaves exactly as before.
type showFilter struct {
	// mine limits runs to the connected user's own.
	mine bool
	// pinnedOnly limits the view to pinned records, whatever their kind.
	pinnedOnly bool
	// status is an AWX status (running, failed, successful); empty is any.
	status string
	// kind is an AWX record type (job, project_update, inventory_update);
	// empty is any.
	kind string
}

func (f showFilter) active() bool { return f != showFilter{} }

// summary names the active narrowing for the count line, so a short list is
// never mistaken for a small instance.
func (f showFilter) summary() string {
	var parts []string
	if f.pinnedOnly {
		parts = append(parts, "pinned")
	}
	if f.mine {
		parts = append(parts, "mine")
	}
	if f.status != "" {
		parts = append(parts, f.status)
	}
	if f.kind != "" {
		parts = append(parts, kindLabels[f.kind])
	}
	return strings.Join(parts, " · ")
}

var kindLabels = map[string]string{
	"job":              "jobs",
	"project_update":   "project updates",
	"inventory_update": "inventory syncs",
}

// choice is one row of the panel: a question and the answers it accepts.
type choice struct {
	field   string
	title   string
	options []option
}

type option struct {
	label string
	value string
}

var (
	ownerChoice = choice{field: "mine", title: "Started by", options: []option{
		{"anyone", ""}, {"me", "yes"},
	}}
	statusChoice = choice{field: "status", title: "Status", options: []option{
		{"any", ""}, {"running", "running"}, {"failed", "failed"}, {"successful", "successful"},
	}}
	kindChoice = choice{field: "kind", title: "Kind", options: []option{
		{"anything", ""}, {"jobs", "job"},
		{"project updates", "project_update"}, {"inventory syncs", "inventory_update"},
	}}
	pinnedChoice = choice{field: "pinned", title: "Pinned", options: []option{
		{"everything", ""}, {"pinned only", "yes"},
	}}
)

// choicesFor is what the panel offers on a tab. Only runs have an owner, a
// status and a kind; everything else can only be narrowed to pins.
func choicesFor(t tab) []choice {
	if t == tabJobs {
		return []choice{ownerChoice, statusChoice, kindChoice, pinnedChoice}
	}
	return []choice{pinnedChoice}
}

// showPanel is the open filter panel. It edits a copy, so leaving with esc
// really does leave everything as it was.
type showPanel struct {
	tab    tab
	cursor int
	draft  showFilter
}

func (f showFilter) get(field string) string {
	switch field {
	case "mine":
		if f.mine {
			return "yes"
		}
	case "pinned":
		if f.pinnedOnly {
			return "yes"
		}
	case "status":
		return f.status
	case "kind":
		return f.kind
	}
	return ""
}

func (f *showFilter) set(field, value string) {
	switch field {
	case "mine":
		f.mine = value != ""
	case "pinned":
		f.pinnedOnly = value != ""
	case "status":
		f.status = value
	case "kind":
		f.kind = value
	}
}

// filterFields is every choice a filter can hold, in one place, so saving and
// restoring stay in step with the panel as choices are added.
var filterFields = []string{"mine", "pinned", "status", "kind"}

// fields flattens a filter for the store.
func (f showFilter) fields() map[string]string {
	out := make(map[string]string, len(filterFields))
	for _, name := range filterFields {
		if v := f.get(name); v != "" {
			out[name] = v
		}
	}
	return out
}

// filterFromFields rebuilds a filter from what was saved. A value the panel no
// longer offers is ignored rather than restored as a filter with no way to
// clear it from the panel.
func filterFromFields(t tab, fields map[string]string) showFilter {
	var f showFilter
	for _, c := range choicesFor(t) {
		v := fields[c.field]
		for _, o := range c.options {
			if o.value == v && v != "" {
				f.set(c.field, v)
			}
		}
	}
	return f
}

// restoreViews puts every tab back to what it was last narrowed to. It runs
// at construction, so the first request a tab makes already carries the
// filter rather than loading an unnarrowed list and replacing it.
func (m *Model) restoreViews() {
	for t := tab(0); t < tabCount; t++ {
		m.show[t] = filterFromFields(t, m.store.View(m.instance, pinGroup(t)))
	}
}

// cycle moves one choice to its next (or previous) option.
func (p *showPanel) cycle(delta int) {
	c := choicesFor(p.tab)[p.cursor]
	at := 0
	for i, o := range c.options {
		if o.value == p.draft.get(c.field) {
			at = i
		}
	}
	next := (at + delta + len(c.options)) % len(c.options)
	p.draft.set(c.field, c.options[next].value)
}

func (m *Model) openShowPanel() {
	m.err, m.notice = nil, ""
	m.panel = showPanel{tab: m.active, draft: m.show[m.active]}
	m.mode = modeShow
}

func (m Model) handleShowKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := choicesFor(m.panel.tab)
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.mode = modeList
		return m, nil
	case "up", "k":
		m.panel.cursor = clamp(m.panel.cursor-1, 0, len(rows)-1)
		return m, nil
	case "down", "j", "tab":
		m.panel.cursor = clamp(m.panel.cursor+1, 0, len(rows)-1)
		return m, nil
	case "right", "l", " ":
		m.panel.cycle(1)
		return m, nil
	case "left", "h":
		m.panel.cycle(-1)
		return m, nil
	case "backspace", "c":
		m.panel.draft = showFilter{}
		return m, nil
	case "enter":
		m.mode = modeList
		t := m.panel.tab
		if m.panel.draft == m.show[t] {
			return m, nil
		}
		m.show[t] = m.panel.draft
		// The narrowing outlives the session, like the pins it can select.
		// A failed write is said out loud: a filter silently not saved is
		// found out tomorrow, when the view is not what it was left as.
		if err := m.store.SetView(m.instance, pinGroup(t), m.show[t].fields()); err != nil {
			m.err = err
		}
		return m, m.reload(t)
	}
	return m, nil
}

// reload throws away what a tab is showing and asks again. What is on screen
// answered a different question, so it cannot be merged or filtered into the
// new one; the search text goes too, since it was searching the old view.
func (m *Model) reload(t tab) tea.Cmd {
	m.err, m.notice = nil, ""
	m.rows[t] = nil
	m.cursor[t], m.offset[t] = 0, 0
	m.next[t], m.count[t] = "", 0
	m.serverQuery[t], m.filters[t] = "", ""
	if m.active == t {
		m.filterInput.SetValue("")
	}
	m.searchSeq[t]++
	switch t {
	case tabTemplates:
		m.templates = nil
	case tabJobs:
		m.jobs = nil
	case tabInventories:
		m.inventories = nil
	case tabProjects:
		m.projects = nil
	}
	return m.load(t, true)
}

// showModal renders the panel.
func (m Model) showModal() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Show " + strings.ToLower(tabNames[m.panel.tab])))
	b.WriteString("\n\n")
	for i, c := range choicesFor(m.panel.tab) {
		cur := m.panel.draft.get(c.field)
		var vals []string
		for _, o := range c.options {
			// The chosen option is bracketed, not merely coloured: colour is
			// dropped when the output is not a terminal, and on a monochrome
			// one, and a panel whose state you cannot read is worse than none.
			switch {
			case o.value == cur && i == m.panel.cursor:
				vals = append(vals, rowSelStyle.Render("["+o.label+"]"))
			case o.value == cur:
				vals = append(vals, mineStyle.Render("["+o.label+"]"))
			default:
				vals = append(vals, dimStyle.Render(" "+o.label+" "))
			}
		}
		marker := "  "
		if i == m.panel.cursor {
			marker = helpKeyStyle.Render("▌ ")
		}
		b.WriteString(marker + cell(helpDescStyle.Render(c.title), 14) + strings.Join(vals, dimStyle.Render("·")))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if !m.store.Any(m.instance, pinGroup(m.panel.tab)) {
		b.WriteString(dimStyle.Render("nothing pinned here yet — p pins the highlighted row") + "\n")
	}
	b.WriteString(dimStyle.Render("↑↓ choose · ←→ or space set · c clears · enter applies · esc cancels"))
	return modalStyle.Width(min(m.width-6, 72)).Render(b.String())
}
