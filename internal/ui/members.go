package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// memberKind says whether a memberList is browsing an inventory's hosts or
// its groups — the two AWX collections a launch's "limit" pattern can name.
// The two share every bit of paging/scroll/select behaviour; only the fetch,
// the row shape and the on-screen noun differ, so one type carries both
// rather than duplicating the whole thing per kind.
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

// memberList is one inventory's hosts or groups, paged in lazily like every
// other list here, plus an in-progress multi-select that becomes a launch's
// default limit once confirmed. It lives on inventoryDetail — expanded and
// browsed inline rather than as its own page, since it only ever makes sense
// in the context of the inventory it belongs to.
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
	// loading is true from the moment the inventory details open until this
	// list's first page lands, so the collapsed preview and the expanded
	// browser can both show a spinner instead of looking like the inventory
	// truly has nothing in it.
	loading bool
	// chosen marks selected rows by AWX id, surviving pagination and
	// re-ordering since it is keyed by id rather than row index.
	chosen map[int]bool
}

func newMemberList(kind memberKind, inv awx.Inventory) memberList {
	return memberList{kind: kind, inventory: inv.ID, invName: inv.Name, loading: true, chosen: map[int]bool{}}
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

// limitSelection is a host/group pick made in an inventory's details,
// waiting for the next launch form against the same inventory to use as its
// limit default. It is not cleared the moment it is used: picking once and
// then opening both an ad hoc command and a template's launch form for the
// same inventory prefills both.
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
