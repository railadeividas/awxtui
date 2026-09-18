package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
	"github.com/railadeividas/awxtui/internal/state"
)

// Pinning is the one thing awxtui knows that AWX does not. A template you
// launch every week is one row among 209, a run you want to come back to is
// one among 170290, and AWX has no bookmark of any kind — so `p` keeps a
// local list, per instance and per kind of record, and `f` can narrow a view
// down to it.

// pinGroup is the state group a tab's records belong to. Ids only identify a
// record within its own collection, so each tab pins into its own list.
func pinGroup(t tab) string {
	switch t {
	case tabJobs:
		return state.GroupRuns
	case tabInventories:
		return state.GroupInventorys
	case tabProjects:
		return state.GroupProjects
	case tabSchedules:
		return state.GroupSchedules
	default:
		return state.GroupTemplates
	}
}

// WithStore gives the model somewhere to keep pins. A model built without one
// still pins for the session; nothing is written, which is what a test or an
// unwritable home directory gets.
func WithStore(s *state.Store) Option {
	return func(m *Model) {
		if s != nil {
			m.store = s
		}
	}
}

// pinned reports whether a record of the current tab is pinned.
func (m Model) pinned(t tab, id int) bool {
	return m.store.Pinned(m.instance, pinGroup(t), id)
}

// togglePin pins or unpins one record of tab t. The write can fail — a full
// disk, an unwritable state directory — and that has to be said rather than
// swallowed, since the whole point of a pin is that it is still there
// tomorrow.
func (m *Model) togglePin(t tab, id int, name, kind string) tea.Cmd {
	pinned, err := m.store.Toggle(m.instance, pinGroup(t), state.Pin{ID: id, Kind: kind, Name: name})
	if err != nil {
		m.err = err
		return nil
	}
	what := name
	if what == "" {
		what = fmt.Sprintf("#%d", id)
	}
	if pinned {
		m.notice = "pinned " + what
	} else {
		m.notice = "unpinned " + what
	}
	// While a pinned-only view is on screen, unpinning removes the row; in
	// any other view only the marker changes.
	if m.show[t].pinnedOnly {
		return m.load(t, true)
	}
	m.rebuild(t)
	return nil
}

// rebuild re-renders one tab's rows from the records already loaded, for a
// change that alters how a row looks rather than which rows there are.
func (m *Model) rebuild(t tab) {
	switch t {
	case tabTemplates:
		m.rows[t] = m.templateRows(m.templates)
	case tabJobs:
		m.rows[t] = m.jobRows(m.jobs)
	case tabInventories:
		m.rows[t] = m.inventoryRows(m.inventories)
	case tabProjects:
		m.rows[t] = m.projectRows(m.projects)
	case tabSchedules:
		m.rows[t] = m.scheduleRows(m.schedules)
	}
}

// togglePinSelected pins whatever is highlighted on the current tab. The name
// comes from the record rather than from the row: the Jobs tab's first cell
// is the id, and every other tab's already carries the pin marker.
func (m *Model) togglePinSelected() tea.Cmd {
	r, ok := m.selected()
	if !ok {
		return nil
	}
	name, kind := "", ""
	switch m.active {
	case tabJobs:
		if j, ok := m.selectedJob(); ok {
			name, kind = j.Name, j.Type
		}
	case tabTemplates:
		if t, ok := m.selectedTemplate(); ok {
			name = t.Name
		}
	case tabInventories:
		if inv, ok := m.selectedInventory(); ok {
			name = inv.Name
		}
	case tabProjects:
		if p, ok := m.selectedProject(); ok {
			name = p.Name
		}
	case tabSchedules:
		if s, ok := m.selectedSchedule(); ok {
			name = s.Name
		}
	}
	return m.togglePin(m.active, r.id, name, kind)
}

// pinMark renders the first cell of a list row, marking a pinned record. The
// mark is a glyph, not a colour: colour is dropped when the output is not a
// terminal, and on a monochrome one.
func (m Model) pinMark(t tab, id int, cell string) string {
	if !m.pinned(t, id) {
		return cell
	}
	return pinStyle.Render("★ ") + cell
}

// pinLabel names what the pin key would do to the highlighted row.
func (m Model) pinLabel() string {
	r, ok := m.selected()
	if ok && m.pinned(m.active, r.id) {
		return "unpin"
	}
	return "pin"
}

// outputPinLabel does the same for the run whose output is open.
func (m Model) outputPinLabel() string {
	if m.store.Pinned(m.instance, state.GroupRuns, m.outputJob.ID) {
		return "unpin"
	}
	return "pin"
}

// jobPinLabel does the same for the run whose launch details are open.
func (m Model) jobPinLabel() string {
	if m.store.Pinned(m.instance, state.GroupRuns, m.job.job.ID) {
		return "unpin"
	}
	return "pin"
}

// inPinnedOrder puts the records AWX returned back into the order the pins
// are kept in — most recently pinned first — rather than AWX's own.
func inPinnedOrder[T any](ids []int, found []T, idOf func(T) int) []T {
	byID := make(map[int]T, len(found))
	for _, rec := range found {
		byID[idOf(rec)] = rec
	}
	out := make([]T, 0, len(found))
	for _, id := range ids {
		if rec, ok := byID[id]; ok {
			out = append(out, rec)
		}
	}
	return out
}

func jobID(j awx.Job) int              { return j.ID }
func templateID(t awx.JobTemplate) int { return t.ID }
func inventoryID(i awx.Inventory) int  { return i.ID }
func projectID(p awx.Project) int      { return p.ID }
func scheduleID(s awx.Schedule) int    { return s.ID }
