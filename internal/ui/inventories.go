package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// inventoryDetail is the state of the inventory details view. Unlike a
// project, an inventory carries no sync status of its own — AWX puts that on
// each inventory source — so opening it needs a follow-up fetch the list
// itself never has to make. Its hosts and groups are fetched the same way,
// and rendered right inside the same view — both always visible, both
// selectable, with a single cursor shared between them (groups first, then
// hosts) so there is nothing to navigate to or back from.
type inventoryDetail struct {
	inventory awx.Inventory
	sources   []awx.InventorySource
	loading   bool
	offset    int // scroll position over the rendered body
	cursor    int // selectable-row cursor, combined across groups then hosts

	hosts  memberList
	groups memberList
}

// totalRows is how many selectable rows (groups, then hosts) are currently
// loaded.
func (d *inventoryDetail) totalRows() int { return len(d.groups.rows) + len(d.hosts.rows) }

// rowAt resolves a combined cursor index to the list it falls in and its
// index within that list's currently loaded rows.
func (d *inventoryDetail) rowAt(i int) (ml *memberList, idx int, ok bool) {
	if i < 0 {
		return nil, 0, false
	}
	if i < len(d.groups.rows) {
		return &d.groups, i, true
	}
	j := i - len(d.groups.rows)
	if j < len(d.hosts.rows) {
		return &d.hosts, j, true
	}
	return nil, 0, false
}

// currentLimit derives the limit selection live from whatever is currently
// checked across groups and hosts. There is no separate "confirm" step —
// toggling a row's checkbox with space is the whole action.
func (d *inventoryDetail) currentLimit() limitSelection {
	var names []string
	for i, r := range d.groups.rows {
		if d.groups.chosen[r.id] {
			names = append(names, d.groups.names[i])
		}
	}
	for i, r := range d.hosts.rows {
		if d.hosts.chosen[r.id] {
			names = append(names, d.hosts.names[i])
		}
	}
	if len(names) == 0 {
		return limitSelection{}
	}
	return limitSelection{inventoryID: d.inventory.ID, names: names}
}

// inventorySourcesMsg carries the sources fetched for the inventory details
// view.
type inventorySourcesMsg struct {
	gen       int
	inventory awx.Inventory
	sources   []awx.InventorySource
}

// openInventoryDetail shows the details of the selected inventory and starts
// fetching its sources plus the first page of its hosts and groups — all
// three render in the same view, so all three are fetched up front.
func (m *Model) openInventoryDetail(inv awx.Inventory) tea.Cmd {
	m.err, m.notice = nil, ""
	m.mode = modeInventory
	m.inventory = inventoryDetail{
		inventory: inv, loading: true,
		hosts:  newMemberList(memberHosts, inv),
		groups: newMemberList(memberGroups, inv),
	}
	return tea.Batch(
		m.fetchInventorySources(inv),
		m.fetchMembers(memberHosts, inv.ID, inv.Name, "", false),
		m.fetchMembers(memberGroups, inv.ID, inv.Name, "", false),
	)
}

func (m *Model) fetchInventorySources(inv awx.Inventory) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		page, err := c.InventorySources(ctx, inv.ID, "", "")
		if err != nil {
			return errMsg{err: err, gen: gen, tab: tabCount}
		}
		return inventorySourcesMsg{gen: gen, inventory: inv, sources: page.Results}
	})
}

// loadMoreMembers pulls ml's next page, capped by maxPages like every other
// lazy list here.
func (m *Model) loadMoreMembers(ml *memberList) tea.Cmd {
	if ml.next == "" || ml.pages >= maxPages {
		return nil
	}
	ml.pages++
	next := ml.next
	ml.next = ""
	return m.fetchMembers(ml.kind, ml.inventory, ml.invName, next, true)
}

// moveInventoryCursor moves the cursor shared by the groups and hosts lists,
// paging in more of whichever one it approaches the end of, and scrolls the
// details view to keep it visible.
func (m *Model) moveInventoryCursor(delta int) tea.Cmd {
	d := &m.inventory
	n := d.totalRows()
	d.cursor = clamp(d.cursor+delta, 0, max(n-1, 0))
	m.syncInventoryOffset()
	if ml, idx, ok := d.rowAt(d.cursor); ok && len(ml.rows)-idx <= loadMoreWithin {
		return m.loadMoreMembers(ml)
	}
	return nil
}

// syncInventoryOffset scrolls the groups/hosts body so the cursor's row
// stays visible, the same way clampInventoryOffset keeps a plain scroll in
// range.
func (m *Model) syncInventoryOffset() {
	_, body, cursorLine, sections := m.inventoryBody(m.inventoryWidth())
	window := m.inventoryBodyWindow()
	off := clampOffset(cursorLine, m.inventory.offset, window, len(body))
	// A pinned section header (see stickyHeader) shrinks how many actual
	// rows fit below it; re-clamp against that smaller window; or the
	// cursor can land in the space the pinned header now occupies and
	// scroll off screen while inventoryModal still thinks it is visible.
	if sticky := stickyHeader(sections, off); len(sticky) > 0 {
		off = clampOffset(cursorLine, off, max(window-len(sticky), 1), len(body))
	}
	m.inventory.offset = off
}

func (m Model) handleInventoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc", "enter":
		m.mode = modeList
		m.inventory = inventoryDetail{}
		return m, nil
	case "up", "k":
		return m, m.moveInventoryCursor(-1)
	case "down", "j":
		return m, m.moveInventoryCursor(1)
	case "pgup", "ctrl+u":
		return m, m.moveInventoryCursor(-m.inventoryBodyWindow() / 2)
	case "pgdown", "ctrl+d":
		return m, m.moveInventoryCursor(m.inventoryBodyWindow() / 2)
	case "home":
		return m, m.moveInventoryCursor(-(1 << 30))
	case "end", "G":
		return m, m.moveInventoryCursor(1 << 30)
	case "g":
		// Jump to the first group — groups render first, so this is the
		// same as "home", but it is the mnemonic a user reaches for.
		return m, m.moveInventoryCursor(-(1 << 30))
	case "h":
		// Jump to the first host, wherever the groups list currently ends.
		m.inventory.cursor = clamp(len(m.inventory.groups.rows), 0, max(m.inventory.totalRows()-1, 0))
		m.syncInventoryOffset()
		return m, nil
	case " ":
		if ml, idx, ok := m.inventory.rowAt(m.inventory.cursor); ok {
			id := ml.rows[idx].id
			ml.chosen[id] = !ml.chosen[id]
			m.limitSel = m.inventory.currentLimit()
		}
		return m, nil
	case "x":
		m.inventory.hosts.chosen = map[int]bool{}
		m.inventory.groups.chosen = map[int]bool{}
		m.limitSel = limitSelection{}
		return m, nil
	case "r":
		inv := m.inventory.inventory
		m.inventory.loading = true
		m.inventory.hosts = newMemberList(memberHosts, inv)
		m.inventory.groups = newMemberList(memberGroups, inv)
		m.inventory.cursor, m.inventory.offset = 0, 0
		return m, tea.Batch(
			m.fetchInventorySources(inv),
			m.fetchMembers(memberHosts, inv.ID, inv.Name, "", false),
			m.fetchMembers(memberGroups, inv.ID, inv.Name, "", false),
		)
	case "a":
		// Unlike the rest of this view, 'a' stays put rather than changing
		// anything about it right away: there is no ad hoc equivalent to
		// switch to, and clearing m.inventory here would render an empty
		// "Inventory #0" until the credential catalogue lands. adHocFormMsg
		// is what actually leaves this view, once the form is ready to show.
		m.err, m.notice = nil, "reading credentials…"
		return m, m.fetchAdHocForm(m.inventory.inventory)
	case "s":
		inv := m.inventory.inventory
		// AWX would answer the same way, but saying it here saves a request
		// and names the reason the modal shows no sources.
		if inv.TotalInventorySources == 0 {
			m.err = fmt.Errorf("inventory %s has no sources to sync", inv.Name)
			return m, nil
		}
		cmd := m.startSync("inventory "+inv.Name, m.syncInventory(inv))
		if cmd == nil {
			return m, nil
		}
		// The update's output replaces the details, which are about to be
		// out of date anyway.
		m.mode = modeList
		m.inventory = inventoryDetail{}
		return m, cmd
	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
}

// inventoryWindow is how many lines the modal can show at once in total,
// header and body combined. The modal spends lines on its border, padding,
// title and key legend.
func (m Model) inventoryWindow() int {
	return max(m.tableHeight()-7, 3)
}

// inventoryBodyWindow is how many of those lines are left for the scrolling
// groups/hosts body once the header (organization, limit, sources — never
// scrolled, so it stays on screen no matter how far down the lists go) has
// taken its share.
func (m Model) inventoryBodyWindow() int {
	header, _, _, _ := m.inventoryBody(m.inventoryWidth())
	// +1 for the blank line inventoryModal always inserts between the
	// header and the body.
	return max(m.inventoryWindow()-len(header)-1, 3)
}

func (m Model) clampInventoryOffset(off int) int {
	_, body, _, _ := m.inventoryBody(m.inventoryWidth())
	return clamp(off, 0, len(body)-m.inventoryBodyWindow())
}

func (m Model) inventoryWidth() int {
	return min(m.width-8, 84)
}

// inventoryModal renders the details of one inventory: a header of its own
// fields and each source's real sync status — always fully shown — and,
// below it, its groups and hosts scrolling on their own so the header never
// scrolls out of view while browsing a long list.
func (m Model) inventoryModal() string {
	inv := m.inventory.inventory
	width := m.inventoryWidth()
	header, body, _, sections := m.inventoryBody(width)
	window := m.inventoryBodyWindow()
	off := clamp(m.inventory.offset, 0, max(len(body)-window, 0))

	// A long scroll inside one table's rows can carry its own "N groups"
	// count and column titles out of view; pin them back at the top of the
	// visible body rather than let the list turn anonymous.
	sticky := stickyHeader(sections, off)
	end := min(off+max(window-len(sticky), 1), len(body))

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Inventory #%d", inv.ID)) + "  " + rowStyle.Bold(true).Render(inv.Name))
	if len(body) > window {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  (%d–%d of %d)", off+1, end, len(body))))
	}
	b.WriteString("\n\n")
	b.WriteString(strings.Join(header, "\n"))
	if len(header) > 0 {
		b.WriteString("\n\n")
	}
	if len(sticky) > 0 {
		b.WriteString(strings.Join(sticky, "\n"))
		b.WriteString("\n")
	}
	b.WriteString(strings.Join(body[off:end], "\n"))
	return modalStyle.Width(width).Render(b.String())
}

// inventoryBody splits the modal's content into a header (organization,
// limit, sources — short, and never scrolled) and a body (the groups and
// hosts tables, which can run well past a screen and scroll on their own),
// the line index within body the combined cursor currently sits on (-1 if
// nothing is loaded to put it on), and each table's own section within body
// so a scroll deep into one can keep its header pinned.
func (m Model) inventoryBody(width int) (header, body []string, cursorLine int, sections []memberSection) {
	inv := m.inventory.inventory
	valueW := max(width-20, 20)
	cursorLine = -1

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

	if d := strings.TrimSpace(inv.Description); d != "" {
		for _, l := range strings.Split(wrapANSI(metaStyle.Render(d), width-6), "\n") {
			lines = append(lines, l)
		}
		lines = append(lines, "")
	}
	field("organization", rowStyle.Render(inv.SummaryFields.Organization.Name))
	if sel := m.limitSel; sel.inventoryID == inv.ID {
		// A selection of even a couple dozen names, ':'-joined, is easily
		// longer than the modal is wide — field()'s normal wrap would turn
		// this one line into a screenful and push the sources and tables
		// below it out of the fixed header's own space. Truncated to a
		// single line plus a count, it stays exactly one line regardless of
		// how many names are behind it.
		suffix := fmt.Sprintf("  (%d selected · x to clear)", len(sel.names))
		avail := max(valueW-len(suffix), 10)
		lines = append(lines, cell(dimStyle.Render("limit"), 16)+cell(rowStyle.Render(sel.limit()), avail)+dimStyle.Render(suffix))
	}
	lines = append(lines, "")

	if m.inventory.loading {
		lines = append(lines, dimStyle.Render("loading sources…"))
	} else if len(m.inventory.sources) == 0 {
		lines = append(lines, dimStyle.Render("no sources — hosts were entered by hand"))
	} else {
		lines = append(lines, titleStyle.Render("sources"))
		for _, s := range m.inventory.sources {
			lines = append(lines, "  "+rowStyle.Render(s.Label())+"  "+statusBadge(s.Status))
			lines = append(lines, cell("", 18)+dimStyle.Render("last sync ")+stamp(s.LastUpdated))
		}
	}
	header = lines
	lines = nil

	// width-6 clears modalStyle's own border and padding, matching the
	// description wrap above; renderTable adds one more column of its own
	// for the row-cursor marker it prepends.
	tableWidth := width - 7
	appendMembers := func(ml memberList, localCursor int) memberSection {
		count := fmt.Sprintf("%d %s", len(ml.rows), ml.kind.noun())
		if ml.count > len(ml.rows) {
			count = fmt.Sprintf("%d of %d %s", len(ml.rows), ml.count, ml.kind.noun())
		}
		if len(ml.chosen) > 0 {
			count = fmt.Sprintf("%d selected · %s", len(ml.chosen), count)
		}
		countLine := dimStyle.Render(count)
		lines = append(lines, countLine)
		if ml.loading {
			lines = append(lines, dimStyle.Render("  fetching "+ml.kind.noun()+"…"))
			return memberSection{header: []string{countLine}, rowStart: len(lines), rowEnd: len(lines)}
		}
		if len(ml.rows) == 0 {
			lines = append(lines, dimStyle.Render("  none"))
			return memberSection{header: []string{countLine}, rowStart: len(lines), rowEnd: len(lines)}
		}
		marked := make([]row, len(ml.rows))
		for i, r := range ml.rows {
			cells := append([]string(nil), r.cells...)
			if ml.chosen[r.id] {
				cells[0] = "[x] " + cells[0]
			} else {
				cells[0] = "[ ] " + cells[0]
			}
			marked[i] = row{id: r.id, cells: cells, search: r.search}
		}
		table := strings.Split(renderTable(memberColumns(ml.kind), marked, localCursor, 0, tableWidth, len(marked)), "\n")
		// table[0] is the column header — part of this section's sticky
		// header, alongside the count line, so both survive a scroll deep
		// into its rows.
		lines = append(lines, table[0])
		sec := memberSection{header: []string{countLine, table[0]}, rowStart: len(lines)}
		for i, l := range table[1:] {
			// a selected row i is loaded row i, which renderTable highlights
			// when i == localCursor.
			if localCursor >= 0 && i == localCursor {
				cursorLine = len(lines)
			}
			lines = append(lines, l)
		}
		sec.rowEnd = len(lines)
		return sec
	}

	groupsCursor := -1
	if m.inventory.cursor < len(m.inventory.groups.rows) {
		groupsCursor = m.inventory.cursor
	}
	groupsSec := appendMembers(m.inventory.groups, groupsCursor)
	lines = append(lines, "")

	hostsCursor := -1
	if idx := m.inventory.cursor - len(m.inventory.groups.rows); idx >= 0 && idx < len(m.inventory.hosts.rows) {
		hostsCursor = idx
	}
	hostsSec := appendMembers(m.inventory.hosts, hostsCursor)

	return header, lines, cursorLine, []memberSection{groupsSec, hostsSec}
}

// memberSection locates one groups/hosts table within the scrolling body:
// its own small header (the "N groups"/"N hosts" count line plus the column
// titles) and the row range that follows it. A section with 70+ rows scrolls
// its own header out of view same as any other line would; stickyHeader
// uses this to pin it back so the count and column names are never lost.
type memberSection struct {
	header   []string
	rowStart int
	rowEnd   int // exclusive
}

// stickyHeader returns a section's header if the body is scrolled to a point
// inside that section's rows but past its own header — i.e. exactly when
// that header would otherwise have scrolled out of view.
func stickyHeader(sections []memberSection, off int) []string {
	for _, sec := range sections {
		headerStart := sec.rowStart - len(sec.header)
		if off > headerStart && off < sec.rowEnd {
			return sec.header
		}
	}
	return nil
}
