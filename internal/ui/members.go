package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// memberKind says whether the members view is browsing an inventory's hosts
// or its groups — the two AWX collections a launch's "limit" pattern can
// name. The two share every bit of paging/scroll/select behaviour; only the
// fetch, the row shape and the on-screen noun differ, so one view carries
// both rather than duplicating the whole thing per kind.
type memberKind int

const (
	memberHosts memberKind = iota
	memberGroups
)

func (k memberKind) noun() string {
	if k == memberGroups {
		return "groups"
	}
	return "hosts"
}

// memberList is the state of the modeMembers view: one inventory's hosts or
// groups, paged in lazily like every other list here, plus an in-progress
// multi-select that becomes a launch's default limit once confirmed.
type memberList struct {
	kind      memberKind
	inventory int
	invName   string
	rows      []row
	names     []string // parallel to rows: the plain name a limit is built from
	cursor    int
	offset    int
	next      string
	count     int
	pages     int
	// loading is true from the moment the view opens until its first page
	// lands, so it can show a spinner instead of an empty table that looks
	// like the inventory truly has nothing in it.
	loading bool
	// chosen marks selected rows by AWX id, surviving pagination and
	// re-ordering since it is keyed by id rather than row index.
	chosen map[int]bool
}

// membersMsg carries one page of an inventory's hosts or groups.
type membersMsg struct {
	pageMeta
	kind        memberKind
	inventory   string
	inventoryID int
	hosts       []awx.Host
	groups      []awx.Group
}

// limitSelection is a host/group pick made in the members view, waiting for
// the next launch form against the same inventory to use as its limit
// default. It is not cleared the moment it is used: picking once and then
// opening both an ad hoc command and a template's launch form for the same
// inventory prefills both.
type limitSelection struct {
	inventoryID int
	names       []string
}

// limit joins the picked names the way AWX's own limit syntax ORs
// alternatives together.
func (s limitSelection) limit() string {
	return strings.Join(s.names, ":")
}

var memberHostColumns = []col{{title: "host"}, {title: "state", width: 10}, {title: "description", width: 32}}
var memberGroupColumns = []col{{title: "group"}, {title: "description", width: 40}}

func memberColumns(kind memberKind) []col {
	if kind == memberGroups {
		return memberGroupColumns
	}
	return memberHostColumns
}

// memberRows builds the rows for whichever kind was fetched, plus the plain
// names a limit is built from — kept apart from the rendered cells so a
// selection marker prepended to a cell never has to be parsed back out.
func memberRows(kind memberKind, hosts []awx.Host, groups []awx.Group) ([]row, []string) {
	if kind == memberGroups {
		rows := make([]row, 0, len(groups))
		names := make([]string, 0, len(groups))
		for _, g := range groups {
			rows = append(rows, row{
				id:     g.ID,
				cells:  []string{g.Name, dimStyle.Render(g.Description)},
				search: strings.ToLower(g.Name + " " + g.Description),
			})
			names = append(names, g.Name)
		}
		return rows, names
	}
	rows := hostRows(hosts)
	names := make([]string, len(hosts))
	for i, h := range hosts {
		names[i] = h.Name
	}
	return rows, names
}

// fetchMembers requests one page of an inventory's hosts or groups.
func (m *Model) fetchMembers(kind memberKind, inventoryID int, name, pageURL string, cont bool) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		meta := pageMeta{cont: cont, gen: gen}
		if kind == memberGroups {
			p, err := c.Groups(ctx, inventoryID, pageURL, "")
			if err != nil {
				return errMsg{err: err, gen: gen, tab: tabCount}
			}
			meta.next, meta.count = p.Next, p.Count
			return membersMsg{pageMeta: meta, kind: kind, inventory: name, inventoryID: inventoryID, groups: p.Results}
		}
		p, err := c.Hosts(ctx, inventoryID, pageURL, "")
		if err != nil {
			return errMsg{err: err, gen: gen, tab: tabCount}
		}
		meta.next, meta.count = p.Next, p.Count
		return membersMsg{pageMeta: meta, kind: kind, inventory: name, inventoryID: inventoryID, hosts: p.Results}
	})
}

// openMembers switches to the members view right away, empty and spinning,
// rather than leaving the inventory details on screen until the request
// comes back — the same reasoning the hosts view always used.
func (m *Model) openMembers(kind memberKind, inv awx.Inventory) tea.Cmd {
	m.err, m.notice = nil, ""
	m.mode = modeMembers
	m.members = memberList{kind: kind, inventory: inv.ID, invName: inv.Name, loading: true, chosen: map[int]bool{}}
	m.inventory = inventoryDetail{}
	return m.fetchMembers(kind, inv.ID, inv.Name, "", false)
}

// loadMoreMembers pulls the next page once the cursor nears the end of what
// is loaded, capped by maxPages like every other lazy list.
func (m *Model) loadMoreMembers() tea.Cmd {
	ml := &m.members
	if ml.next == "" || ml.pages >= maxPages || len(ml.rows)-ml.cursor > loadMoreWithin {
		return nil
	}
	ml.pages++
	next := ml.next
	ml.next = ""
	return m.fetchMembers(ml.kind, ml.inventory, ml.invName, next, true)
}

func (m Model) handleMembersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ml := &m.members
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc":
		// The members view is only reached from an inventory's details, so
		// back goes there rather than all the way out to the list.
		if inv, ok := m.inventoryByID(ml.inventory); ok {
			return m, m.openInventoryDetail(inv)
		}
		m.mode = modeList
		return m, nil
	case "up", "k":
		return m, m.moveCursor(-1)
	case "down", "j":
		return m, m.moveCursor(1)
	case "pgup", "ctrl+u":
		return m, m.moveCursor(-m.tableHeight() / 2)
	case "pgdown", "ctrl+d":
		return m, m.moveCursor(m.tableHeight() / 2)
	case " ":
		if ml.cursor < len(ml.rows) {
			id := ml.rows[ml.cursor].id
			ml.chosen[id] = !ml.chosen[id]
		}
		return m, nil
	case "a":
		for _, r := range ml.rows {
			ml.chosen[r.id] = true
		}
		return m, nil
	case "c":
		ml.chosen = map[int]bool{}
		return m, nil
	case "enter":
		var names []string
		for i, r := range ml.rows {
			if ml.chosen[r.id] {
				names = append(names, ml.names[i])
			}
		}
		if len(names) == 0 {
			m.limitSel = limitSelection{}
		} else {
			m.limitSel = limitSelection{inventoryID: ml.inventory, names: names}
			m.notice = "limit: " + m.limitSel.limit()
		}
		if inv, ok := m.inventoryByID(ml.inventory); ok {
			return m, m.openInventoryDetail(inv)
		}
		m.mode = modeList
		return m, nil
	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
}

// membersBody renders the members view: a header naming the inventory and
// count, then the table with a selection marker in the leading cell.
func (m Model) membersBody() string {
	ml := m.members
	var b strings.Builder
	count := fmt.Sprintf("%d %s", len(ml.rows), ml.kind.noun())
	if ml.count > len(ml.rows) {
		count = fmt.Sprintf("%d of %d %s", len(ml.rows), ml.count, ml.kind.noun())
	}
	if len(ml.chosen) > 0 {
		count = fmt.Sprintf("%d selected · %s", len(ml.chosen), count)
	}
	b.WriteString(m.spread(dimStyle.Render("esc to go back"), dimStyle.Render(count)))
	b.WriteString("\n")
	h := m.tableHeight()
	if ml.loading {
		b.WriteString("\n  " + m.spin.View() + dimStyle.Render(" fetching "+ml.kind.noun()+"…"))
		b.WriteString(strings.Repeat("\n", max(0, h-1)))
		return b.String()
	}

	marked := make([]row, len(ml.rows))
	for i, r := range ml.rows {
		cells := make([]string, len(r.cells))
		copy(cells, r.cells)
		if ml.chosen[r.id] {
			cells[0] = "[x] " + cells[0]
		} else {
			cells[0] = "[ ] " + cells[0]
		}
		marked[i] = row{id: r.id, cells: cells, search: r.search}
	}
	table := renderTable(memberColumns(ml.kind), marked, ml.cursor, ml.offset, m.width-1, h)
	b.WriteString(table)
	b.WriteString(strings.Repeat("\n", max(0, h-countLines(table)+1)))
	return b.String()
}
