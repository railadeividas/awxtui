package ui

import (
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
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
	// status is a set of AWX statuses (running, failed, successful…), any of
	// which match; empty is any. A run is usually worth looking at because it
	// failed or is still running, and those are two different statuses.
	status []string
	// kind is a set of AWX record types (job, project_update,
	// inventory_update), any of which match; empty is any.
	kind []string
	// startedBy is a fragment of a username, matched case-insensitively.
	// "mine" only ever means the connected user; a deploy bot or a colleague
	// needs their own name typed in.
	startedBy string
}

// equal reports whether two filters hold the same narrowing. showFilter is
// not comparable with == once it holds a slice.
func (f showFilter) equal(g showFilter) bool {
	return f.mine == g.mine && f.pinnedOnly == g.pinnedOnly &&
		f.startedBy == g.startedBy &&
		slices.Equal(f.status, g.status) && slices.Equal(f.kind, g.kind)
}

func (f showFilter) active() bool { return !f.equal(showFilter{}) }

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
	if f.startedBy != "" {
		parts = append(parts, "by "+f.startedBy)
	}
	if len(f.status) > 0 {
		parts = append(parts, strings.Join(f.status, "/"))
	}
	if len(f.kind) > 0 {
		labels := make([]string, len(f.kind))
		for i, k := range f.kind {
			labels[i] = kindLabels[k]
		}
		parts = append(parts, strings.Join(labels, "/"))
	}
	return strings.Join(parts, " · ")
}

var kindLabels = map[string]string{
	"job":              "jobs",
	"project_update":   "project updates",
	"inventory_update": "inventory syncs",
}

// choiceKind is how a row's options are picked. kindCycle and kindMulti both
// offer a fixed option list; kindOwner is a hybrid that starts as an
// anyone/me toggle and turns into free text the moment a character is typed.
type choiceKind int

const (
	kindCycle choiceKind = iota
	kindMulti
	kindOwner
)

// choice is one row of the panel: a question and the answers it accepts.
type choice struct {
	field   string
	title   string
	kind    choiceKind
	options []option
}

type option struct {
	label string
	value string
}

var (
	// ownerChoice is one row that does two jobs: empty, it toggles between
	// anyone and the connected user with ←→/space, same as any other cycle
	// row; the moment a character is typed it becomes a free-text username
	// fragment instead, because "me" can only ever mean the connected user
	// and finding a colleague's or a deploy bot's runs needs their name.
	ownerChoice = choice{field: "mine", title: "Started by", kind: kindOwner, options: []option{
		{"anyone", ""}, {"me", "yes"},
	}}
	// statusChoice leads with "any", which clears every selected status. The
	// empty set is what AWX uses for an unfiltered status query, but making it
	// a visible choice gives the user a direct way to return to every run.
	statusChoice = choice{field: "status", title: "Status", kind: kindMulti, options: []option{
		{"any", ""}, {"pending", "pending"}, {"running", "running"},
		{"failed", "failed"}, {"successful", "successful"},
	}}
	// kindChoice leads with "any", a value of "" like any other row's blank
	// option: selecting it does not add "" to the set, it clears whatever is
	// selected, since an empty set already means any kind. "everything"
	// (Pinned's wording for the same idea) does not fit here alongside
	// "project updates" and "inventory syncs" without wrapping the row.
	kindChoice = choice{field: "kind", title: "Kind", kind: kindMulti, options: []option{
		{"any", ""}, {"jobs", "job"},
		{"project updates", "project_update"}, {"inventory syncs", "inventory_update"},
	}}
	pinnedChoice = choice{field: "pinned", title: "Pinned", kind: kindCycle, options: []option{
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
	// optCursor is which option is highlighted on a kindMulti row; left and
	// right move it, space toggles it. It is not the selection itself — a
	// multi row can have several options selected at once.
	optCursor      int
	draft          showFilter
	startedByInput textinput.Model
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
		return strings.Join(f.status, ",")
	case "kind":
		return strings.Join(f.kind, ",")
	case "startedBy":
		return f.startedBy
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
		f.status = nil
		if value != "" {
			f.status = strings.Split(value, ",")
		}
	case "kind":
		f.kind = nil
		if value != "" {
			f.kind = strings.Split(value, ",")
		}
	case "startedBy":
		f.startedBy = value
	}
}

// multi returns a pointer to the set behind a kindMulti field, so toggling
// and reading it can be generic over which row (status, kind…) it is.
func (f *showFilter) multi(field string) *[]string {
	switch field {
	case "status":
		return &f.status
	case "kind":
		return &f.kind
	}
	return nil
}

// toggleMember adds or removes one value from the set, keeping it sorted so
// two filters holding the same members always compare equal regardless of
// the order they were toggled in.
func toggleMember(cur []string, v string) []string {
	if i := slices.Index(cur, v); i >= 0 {
		return slices.Delete(slices.Clone(cur), i, i+1)
	}
	out := append(slices.Clone(cur), v)
	sort.Strings(out)
	return out
}

// filterFields is every choice a filter can hold, in one place, so saving and
// restoring stay in step with the panel as choices are added.
var filterFields = []string{"mine", "pinned", "status", "kind", "startedBy"}

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
		if v == "" {
			continue
		}
		switch c.kind {
		case kindMulti:
			var kept []string
			for _, part := range strings.Split(v, ",") {
				for _, o := range c.options {
					if o.value == part {
						kept = append(kept, part)
					}
				}
			}
			if len(kept) > 0 {
				sort.Strings(kept)
				f.set(c.field, strings.Join(kept, ","))
			}
		default:
			for _, o := range c.options {
				if o.value == v && v != "" {
					f.set(c.field, v)
				}
			}
		}
	}
	// startedBy has no row of its own to validate against — it shares the
	// "Started by" row with the mine toggle, as free text with no fixed
	// options — so it is restored unconditionally, the way the mine toggle
	// no longer needs to be once it is typed over.
	if t == tabJobs {
		f.startedBy = fields["startedBy"]
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

// cycle moves the current row's single value to its next (or previous)
// option. It only makes sense for a kindCycle row.
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

func newStartedByInput() textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 64
	in.TextStyle = inputStyle
	in.Cursor.SetMode(cursor.CursorStatic)
	return in
}

func (m *Model) openShowPanel() {
	m.err, m.notice = nil, ""
	in := newStartedByInput()
	in.SetValue(m.show[m.active].startedBy)
	in.CursorEnd()
	m.panel = showPanel{tab: m.active, draft: m.show[m.active], startedByInput: in}
	if choicesFor(m.active)[0].kind == kindOwner {
		m.panel.startedByInput.Focus()
	}
	m.mode = modeShow
}

// moveShowCursor moves the row cursor, blurring the owner row's input if it
// is being left and focusing it if it is being entered — typing must work
// the instant the row is landed on, with no separate step to start it.
func (m *Model) moveShowCursor(delta int) tea.Cmd {
	rows := choicesFor(m.panel.tab)
	if rows[m.panel.cursor].kind == kindOwner {
		m.panel.startedByInput.Blur()
	}
	m.panel.cursor = clamp(m.panel.cursor+delta, 0, len(rows)-1)
	m.panel.optCursor = 0
	if rows[m.panel.cursor].kind == kindOwner {
		m.panel.startedByInput.CursorEnd()
		return m.panel.startedByInput.Focus()
	}
	return nil
}

// applyShowPanel commits the draft, or does nothing if it did not change.
func (m Model) applyShowPanel() (tea.Model, tea.Cmd) {
	m.mode = modeList
	t := m.panel.tab
	if m.panel.draft.equal(m.show[t]) {
		return m, nil
	}
	m.show[t] = m.panel.draft
	// The narrowing outlives the session, like the pins it can select. A
	// failed write is said out loud: a filter silently not saved is found out
	// tomorrow, when the view is not what it was left as.
	if err := m.store.SetView(m.instance, pinGroup(t), m.show[t].fields()); err != nil {
		m.err = err
	}
	return m, m.reload(t)
}

func (m Model) handleShowKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := choicesFor(m.panel.tab)
	cur := rows[m.panel.cursor]

	// The owner row reserves only navigation and exit keys, plus ←→/space to
	// toggle anyone/me while its text is empty — exactly like any other
	// cycle row. The moment it holds text, arrow keys and space stop being
	// reserved and become ordinary editing inside the field, the same split
	// the launch form uses between its choice rows and its text fields.
	if cur.kind == kindOwner {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.mode = modeList
			return m, nil
		case "enter":
			return m.applyShowPanel()
		case "up":
			return m, m.moveShowCursor(-1)
		case "down", "tab":
			return m, m.moveShowCursor(1)
		case "shift+tab":
			return m, m.moveShowCursor(-1)
		}
		if m.panel.startedByInput.Value() == "" {
			switch msg.String() {
			case "right", "left":
				m.panel.cycle(1)
				return m, nil
			case "backspace":
				m.panel.draft = showFilter{}
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.panel.startedByInput, cmd = m.panel.startedByInput.Update(msg)
		m.panel.draft.startedBy = m.panel.startedByInput.Value()
		if m.panel.draft.startedBy != "" {
			m.panel.draft.mine = false
		}
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.mode = modeList
		return m, nil
	case "up", "k":
		return m, m.moveShowCursor(-1)
	case "down", "j", "tab":
		return m, m.moveShowCursor(1)
	case "right", "l":
		if cur.kind == kindMulti {
			m.panel.optCursor = (m.panel.optCursor + 1) % len(cur.options)
		} else {
			m.panel.cycle(1)
		}
		return m, nil
	case "left", "h":
		if cur.kind == kindMulti {
			m.panel.optCursor = (m.panel.optCursor - 1 + len(cur.options)) % len(cur.options)
		} else {
			m.panel.cycle(-1)
		}
		return m, nil
	case " ":
		// Only a multi row toggles: on a cycle row, space advancing one step
		// looked identical to "toggle" for a two-option row (Pinned) but not
		// for a row with more choices, where it just looked like right with
		// an extra key.
		if cur.kind == kindMulti {
			v := cur.options[m.panel.optCursor].value
			set := m.panel.draft.multi(cur.field)
			if v == "" {
				// "any" is not a member to add — an empty value already
				// means any, so selecting it clears the set.
				*set = nil
			} else {
				*set = toggleMember(*set, v)
			}
		}
		return m, nil
	case "backspace", "c":
		m.panel.draft = showFilter{}
		m.panel.startedByInput.SetValue("")
		return m, nil
	case "enter":
		return m.applyShowPanel()
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
	case tabWorkflows:
		m.workflows = nil
	}
	return m.load(t, true)
}

// showModal renders the panel.
func (m Model) showModal() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Show " + strings.ToLower(tabNames[m.panel.tab])))
	b.WriteString("\n\n")
	for i, c := range choicesFor(m.panel.tab) {
		marker := "  "
		if i == m.panel.cursor {
			marker = helpKeyStyle.Render("▌ ")
		}
		if c.kind == kindOwner && m.panel.startedByInput.Value() != "" {
			b.WriteString(marker + cell(helpDescStyle.Render(c.title), 14) + m.panel.startedByInput.View())
			b.WriteString("\n")
			continue
		}
		var vals []string
		for oi, o := range c.options {
			switch c.kind {
			case kindMulti:
				current := *m.panel.draft.multi(c.field)
				// "everything" is not a member; it reads as selected when
				// nothing else is, the same blank-means-any value every
				// other row uses.
				selected := o.value == "" && len(current) == 0 ||
					o.value != "" && slices.Contains(current, o.value)
				focused := i == m.panel.cursor && oi == m.panel.optCursor
				label := " " + o.label + " "
				if selected {
					label = "[" + o.label + "]"
				}
				switch {
				case focused:
					vals = append(vals, rowSelStyle.Render(label))
				case selected:
					vals = append(vals, mineStyle.Render(label))
				default:
					vals = append(vals, dimStyle.Render(label))
				}
			default:
				cur := m.panel.draft.get(c.field)
				switch {
				// The chosen option is bracketed, not merely coloured: colour
				// is dropped when the output is not a terminal, and on a
				// monochrome one, and a panel whose state you cannot read is
				// worse than none.
				case o.value == cur && i == m.panel.cursor:
					vals = append(vals, rowSelStyle.Render("["+o.label+"]"))
				case o.value == cur:
					vals = append(vals, mineStyle.Render("["+o.label+"]"))
				default:
					vals = append(vals, dimStyle.Render(" "+o.label+" "))
				}
			}
		}
		row := strings.Join(vals, dimStyle.Render("·"))
		if c.kind == kindOwner {
			row += dimStyle.Render(" · type a name…")
		}
		b.WriteString(marker + cell(helpDescStyle.Render(c.title), 14) + row)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if !m.store.Any(m.instance, pinGroup(m.panel.tab)) {
		b.WriteString(dimStyle.Render("nothing pinned here yet — p pins the highlighted row") + "\n")
	}
	return modalStyle.Width(min(m.width-6, 72)).Render(strings.TrimSuffix(b.String(), "\n"))
}
