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
// itself never has to make.
type inventoryDetail struct {
	inventory awx.Inventory
	sources   []awx.InventorySource
	loading   bool
	offset    int

	// preview holds the first few host and group names, shown inline so a
	// quick check does not always need the dedicated, drill-in members view.
	hostNames      []string
	hostTotal      int
	groupNames     []string
	groupTotal     int
	previewLoading bool
	previewErr     error
}

// inventorySourcesMsg carries the sources fetched for the inventory details
// view.
type inventorySourcesMsg struct {
	gen       int
	inventory awx.Inventory
	sources   []awx.InventorySource
}

// inventoryPreviewMsg carries the first page of an inventory's hosts and
// groups, fetched alongside its sources so the details modal can name a few
// of each rather than showing bare counts alone.
type inventoryPreviewMsg struct {
	gen         int
	inventoryID int
	hostNames   []string
	hostTotal   int
	groupNames  []string
	groupTotal  int
	err         error
}

// openInventoryDetail shows the details of the selected inventory and starts
// the fetch of its sources and its host/group name preview.
func (m *Model) openInventoryDetail(inv awx.Inventory) tea.Cmd {
	m.err, m.notice = nil, ""
	m.mode = modeInventory
	m.inventory = inventoryDetail{inventory: inv, loading: true, previewLoading: true}
	return tea.Batch(m.fetchInventorySources(inv), m.fetchInventoryPreview(inv))
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

// fetchInventoryPreview reads a handful of an inventory's hosts and groups by
// name. It never fails the details view over this: a preview error is shown
// on its own line rather than replacing the sources the modal is mainly
// there for.
func (m *Model) fetchInventoryPreview(inv awx.Inventory) tea.Cmd {
	c, gen := m.client, m.gen
	return m.request(func() tea.Msg {
		ctx, cancel := cmdCtx()
		defer cancel()
		hostPage, err := c.FirstHosts(ctx, inv.ID)
		if err != nil {
			return inventoryPreviewMsg{gen: gen, inventoryID: inv.ID, err: err}
		}
		groupPage, err := c.FirstGroups(ctx, inv.ID)
		if err != nil {
			return inventoryPreviewMsg{gen: gen, inventoryID: inv.ID, err: err}
		}
		hostNames := make([]string, len(hostPage.Results))
		for i, h := range hostPage.Results {
			hostNames[i] = h.Name
		}
		groupNames := make([]string, len(groupPage.Results))
		for i, g := range groupPage.Results {
			groupNames[i] = g.Name
		}
		return inventoryPreviewMsg{
			gen: gen, inventoryID: inv.ID,
			hostNames: hostNames, hostTotal: hostPage.Count,
			groupNames: groupNames, groupTotal: groupPage.Count,
		}
	})
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
		return m, m.openMembers(memberHosts, m.inventory.inventory)
	case "g":
		return m, m.openMembers(memberGroups, m.inventory.inventory)
	case "x":
		if m.limitSel.inventoryID == m.inventory.inventory.ID {
			m.limitSel = limitSelection{}
			m.notice = "limit selection cleared"
		}
		return m, nil
	case "a":
		// Unlike 'h', this stays on the details view rather than switching
		// right away: there is no ad hoc equivalent of the hosts view to
		// switch to and spin in, and clearing m.inventory here while staying
		// in modeInventory would render an empty "Inventory #0" until the
		// credential catalogue lands. adHocFormMsg is what actually leaves
		// this view, once the form is ready to show.
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

// inventoryWindow is how many body lines the modal can show at once. The
// modal spends lines on its border, padding, title and key legend.
func (m Model) inventoryWindow() int {
	return max(m.tableHeight()-7, 3)
}

func (m Model) clampInventoryOffset(off int) int {
	return clamp(off, 0, len(m.inventoryBody(m.inventoryWidth()))-m.inventoryWindow())
}

func (m Model) inventoryWidth() int {
	return min(m.width-8, 84)
}

// inventoryModal renders the details of one inventory: its own fields, plus
// each source's real sync status, which is what "healthy" on the list can't
// show.
func (m Model) inventoryModal() string {
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

// previewSuffix renders the "web-01, web-02, … (+82 more · h to browse and
// select)" text that follows a hosts/groups count, so a quick check does not
// always need the dedicated members view. key names the key that opens it.
func previewSuffix(d inventoryDetail, names []string, total int, key string) string {
	if d.previewLoading {
		return dimStyle.Render("  loading…")
	}
	if d.previewErr != nil {
		return dimStyle.Render("  could not read names")
	}
	if len(names) == 0 {
		return ""
	}
	more := total - len(names)
	suffix := fmt.Sprintf("  · %s to browse and select", key)
	if more > 0 {
		suffix = fmt.Sprintf("  (+%d more%s)", more, suffix)
	}
	return dimStyle.Render("  "+strings.Join(names, ", ")) + dimStyle.Render(suffix)
}

// inventoryBody is the scrollable content of the modal, one string per line.
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
	field("hosts", rowStyle.Render(fmt.Sprintf("%d", inv.TotalHosts))+previewSuffix(m.inventory, m.inventory.hostNames, m.inventory.hostTotal, "h"))
	field("groups", rowStyle.Render(fmt.Sprintf("%d", inv.TotalGroups))+previewSuffix(m.inventory, m.inventory.groupNames, m.inventory.groupTotal, "g"))
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
