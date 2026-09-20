package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
)

// detailFocus says what an inventory's details view currently shows: its own
// fields, or one of its member lists expanded for browsing and selection.
// There is no separate page for hosts and groups — they live inside the same
// details view, entered and left with h/g/esc, so picking one never costs a
// trip back out to the inventories list.
type detailFocus int

const (
	focusFields detailFocus = iota
	focusHosts
	focusGroups
)

// inventoryDetail is the state of the inventory details view. Unlike a
// project, an inventory carries no sync status of its own — AWX puts that on
// each inventory source — so opening it needs a follow-up fetch the list
// itself never has to make. Its hosts and groups are fetched the same way,
// eagerly, so the fields view can name a few of each and expanding either is
// instant rather than a fresh, spinning fetch.
type inventoryDetail struct {
	inventory awx.Inventory
	sources   []awx.InventorySource
	loading   bool
	offset    int

	focus  detailFocus
	hosts  memberList
	groups memberList
}

// focusedMembers is whichever of hosts/groups is currently expanded. Only
// meaningful when focus != focusFields.
func (d *inventoryDetail) focusedMembers() *memberList {
	if d.focus == focusGroups {
		return &d.groups
	}
	return &d.hosts
}

// inventorySourcesMsg carries the sources fetched for the inventory details
// view.
type inventorySourcesMsg struct {
	gen       int
	inventory awx.Inventory
	sources   []awx.InventorySource
}

// openInventoryDetail shows the details of the selected inventory and starts
// fetching its sources plus the first page of its hosts and groups.
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

// loadMoreMembers pulls the next page of whichever of hosts/groups is
// expanded, once the cursor nears the end of what is loaded — capped by
// maxPages like every other lazy list.
func (m *Model) loadMoreMembers() tea.Cmd {
	ml := m.inventory.focusedMembers()
	if ml.next == "" || ml.pages >= maxPages || len(ml.rows)-ml.cursor > loadMoreWithin {
		return nil
	}
	ml.pages++
	next := ml.next
	ml.next = ""
	return m.fetchMembers(ml.kind, ml.inventory, ml.invName, next, true)
}

func (m Model) handleInventoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.inventory.focus != focusFields {
		return m.handleInventoryMembersKey(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q", "esc", "enter":
		m.mode = modeList
		m.inventory = inventoryDetail{}
		return m, nil
	case "up", "k":
		m.inventory.offset = m.clampInventoryOffset(m.inventory.offset - 1)
		return m, nil
	case "down", "j":
		m.inventory.offset = m.clampInventoryOffset(m.inventory.offset + 1)
		return m, nil
	case "pgup", "ctrl+u":
		m.inventory.offset = m.clampInventoryOffset(m.inventory.offset - m.inventoryWindow()/2)
		return m, nil
	case "pgdown", "ctrl+d":
		m.inventory.offset = m.clampInventoryOffset(m.inventory.offset + m.inventoryWindow()/2)
		return m, nil
	case "home":
		m.inventory.offset = 0
		return m, nil
	case "end", "G":
		m.inventory.offset = m.clampInventoryOffset(1 << 30)
		return m, nil
	case "r":
		m.inventory.loading = true
		return m, m.fetchInventorySources(m.inventory.inventory)
	case "h":
		m.inventory.focus = focusHosts
		return m, nil
	case "g":
		m.inventory.focus = focusGroups
		return m, nil
	case "x":
		if m.limitSel.inventoryID == m.inventory.inventory.ID {
			m.limitSel = limitSelection{}
			m.notice = "limit selection cleared"
		}
		return m, nil
	case "a":
		// Unlike 'h'/'g', this stays on the details view rather than
		// expanding right away: there is no ad hoc equivalent of a members
		// list to expand into, and clearing m.inventory here would render an
		// empty "Inventory #0" until the credential catalogue lands.
		// adHocFormMsg is what actually leaves this view, once the form is
		// ready to show.
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

// handleInventoryMembersKey drives an expanded hosts/groups list: moving the
// cursor, toggling selection and confirming a limit — all inside the same
// details view, never switching mode.
func (m Model) handleInventoryMembersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ml := m.inventory.focusedMembers()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.inventory.focus = focusFields
		return m, nil
	case "h":
		if m.inventory.focus == focusHosts {
			m.inventory.focus = focusFields
		} else {
			m.inventory.focus = focusHosts
		}
		return m, nil
	case "g":
		if m.inventory.focus == focusGroups {
			m.inventory.focus = focusFields
		} else {
			m.inventory.focus = focusGroups
		}
		return m, nil
	case "up", "k":
		return m, m.moveCursor(-1)
	case "down", "j":
		return m, m.moveCursor(1)
	case "pgup", "ctrl+u":
		return m, m.moveCursor(-m.membersWindow() / 2)
	case "pgdown", "ctrl+d":
		return m, m.moveCursor(m.membersWindow() / 2)
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
		m.inventory.focus = focusFields
		return m, nil
	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
}

// inventoryWindow is how many body lines the modal can show at once. The
// modal spends lines on its border, padding, title and key legend.
func (m Model) inventoryWindow() int {
	return max(m.tableHeight()-7, 3)
}

// membersWindow is how many rows an expanded hosts/groups list can show at
// once, inside the same modal the collapsed details view uses.
func (m Model) membersWindow() int {
	return max(m.inventoryWindow()-2, 3)
}

func (m Model) clampInventoryOffset(off int) int {
	return clamp(off, 0, len(m.inventoryBody(m.inventoryWidth()))-m.inventoryWindow())
}

func (m Model) inventoryWidth() int {
	return min(m.width-8, 84)
}

// inventoryModal renders the inventory details view: either its own fields
// plus each source's real sync status, or — while a members list is
// expanded — that list, inside the same modal.
func (m Model) inventoryModal() string {
	if m.inventory.focus != focusFields {
		return m.inventoryMembersModal()
	}
	inv := m.inventory.inventory
	width := m.inventoryWidth()
	lines := m.inventoryBody(width)
	window := m.inventoryWindow()
	off := clamp(m.inventory.offset, 0, max(len(lines)-window, 0))
	end := min(off+window, len(lines))

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Inventory #%d", inv.ID)) + "  " + rowStyle.Bold(true).Render(inv.Name))
	if len(lines) > window {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  (%d–%d of %d lines)", off+1, end, len(lines))))
	}
	b.WriteString("\n\n")
	b.WriteString(strings.Join(lines[off:end], "\n"))
	return modalStyle.Width(width).Render(b.String())
}

// inventoryMembersModal renders an inventory's hosts or groups expanded for
// browsing and selection, inside the same modal the collapsed details use —
// there is no separate page to switch to or back from.
func (m Model) inventoryMembersModal() string {
	inv := m.inventory.inventory
	ml := *m.inventory.focusedMembers()
	width := m.inventoryWidth()
	h := m.membersWindow()

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Inventory #%d", inv.ID)) + "  " + rowStyle.Bold(true).Render(inv.Name))
	b.WriteString(dimStyle.Render(" ▸ " + ml.kind.noun()))
	b.WriteString("\n\n")

	count := fmt.Sprintf("%d %s", len(ml.rows), ml.kind.noun())
	if ml.count > len(ml.rows) {
		count = fmt.Sprintf("%d of %d %s", len(ml.rows), ml.count, ml.kind.noun())
	}
	if len(ml.chosen) > 0 {
		count = fmt.Sprintf("%d selected · %s", len(ml.chosen), count)
	}
	b.WriteString(dimStyle.Render(count))
	b.WriteString("\n")

	if ml.loading {
		b.WriteString("\n  " + m.spin.View() + dimStyle.Render(" fetching "+ml.kind.noun()+"…"))
		return modalStyle.Width(width).Render(b.String())
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
	// width-6 clears modalStyle's own border and padding, matching the
	// description wrap elsewhere in this modal; renderTable adds one more
	// column of its own for the row cursor marker it prepends, so the
	// budget handed to it must leave room for that too.
	b.WriteString(renderTable(memberColumns(ml.kind), marked, ml.cursor, ml.offset, width-7, h))
	return modalStyle.Width(width).Render(b.String())
}

// fieldPreview renders the "web-01, web-02, … (+82 more · h to browse and
// select)" text that follows a hosts/groups count, so a quick check does not
// always need to expand the list. key names the key that expands it.
func fieldPreview(ml memberList, key string) string {
	if ml.loading {
		return dimStyle.Render("  loading…")
	}
	if len(ml.names) == 0 {
		return ""
	}
	n := min(len(ml.names), 8)
	more := ml.count - n
	suffix := fmt.Sprintf("  · %s to browse and select", key)
	if more > 0 {
		suffix = fmt.Sprintf("  (+%d more%s)", more, suffix)
	}
	return dimStyle.Render("  "+strings.Join(ml.names[:n], ", ")) + dimStyle.Render(suffix)
}

// inventoryBody is the scrollable content of the modal's fields view, one
// string per line.
func (m Model) inventoryBody(width int) []string {
	inv := m.inventory.inventory
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

	if d := strings.TrimSpace(inv.Description); d != "" {
		for _, l := range strings.Split(wrapANSI(metaStyle.Render(d), width-6), "\n") {
			lines = append(lines, l)
		}
		lines = append(lines, "")
	}

	field("organization", rowStyle.Render(inv.SummaryFields.Organization.Name))
	field("hosts", rowStyle.Render(fmt.Sprintf("%d", inv.TotalHosts))+fieldPreview(m.inventory.hosts, "h"))
	field("groups", rowStyle.Render(fmt.Sprintf("%d", inv.TotalGroups))+fieldPreview(m.inventory.groups, "g"))
	if sel := m.limitSel; sel.inventoryID == inv.ID {
		field("limit", rowStyle.Render(sel.limit())+dimStyle.Render("  (x to clear)"))
	}
	lines = append(lines, "")

	if m.inventory.loading {
		lines = append(lines, dimStyle.Render("loading sources…"))
		return lines
	}
	if len(m.inventory.sources) == 0 {
		lines = append(lines, dimStyle.Render("no sources — hosts were entered by hand"))
		return lines
	}
	lines = append(lines, titleStyle.Render("sources"))
	for _, s := range m.inventory.sources {
		lines = append(lines, "  "+rowStyle.Render(s.Label())+"  "+statusBadge(s.Status))
		lines = append(lines, cell("", 18)+dimStyle.Render("last sync ")+stamp(s.LastUpdated))
	}
	return lines
}
