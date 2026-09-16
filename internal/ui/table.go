package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// col is a table column. A width of 0 means "take a share of whatever is left".
type col struct {
	title string
	width int
}

// row is one line of a table plus the AWX object id it points at.
type row struct {
	id    int
	cells []string
	// search is the lowercased text the filter matches against.
	search string
}

const (
	// minFlex is the narrowest a flexible (name) column may become before a
	// column gets dropped entirely.
	minFlex = 12
	// desiredFlex is the width a flexible column is fed by shrinking fixed
	// columns, before they hit fixedFloor.
	desiredFlex = 28
	fixedFloor  = 9
)

// widths distributes total across cols: fixed columns keep their width and
// flexible ones absorb the remainder. When the terminal is too narrow, columns
// are dropped from the right (width -1 means "hidden") so the leading columns
// stay readable instead of overflowing the screen.
func widths(cols []col, total int) []int {
	const gap = 2
	out := make([]int, len(cols))
	for i := range out {
		out[i] = -1
	}

	shown := len(cols)
	for ; shown > 1; shown-- {
		fixed, flex := 0, 0
		for _, c := range cols[:shown] {
			if c.width > 0 {
				fixed += c.width
			} else {
				flex++
			}
		}
		avail := total - fixed - gap*(shown-1)
		if flex == 0 && avail >= 0 {
			break
		}
		if flex > 0 && avail/flex >= minFlex {
			break
		}
	}

	flex := 0
	for i, c := range cols[:shown] {
		out[i] = c.width
		if c.width == 0 {
			flex++
		}
	}
	avail := func() int {
		left := total - gap*(shown-1)
		for _, w := range out[:shown] {
			if w > 0 {
				left -= w
			}
		}
		return left
	}

	// Narrow terminal: borrow width from fixed columns so the flexible column
	// (usually the name) keeps enough room to be useful.
	if flex > 0 {
		for i, c := range cols[:shown] {
			need := desiredFlex*flex - avail()
			if c.width <= 0 || need <= 0 {
				continue
			}
			give := min(c.width-min(c.width, fixedFloor), need)
			out[i] = c.width - give
		}
	}
	if flex > 0 {
		each := avail() / flex
		for i, c := range cols[:shown] {
			if c.width == 0 {
				out[i] = each
			}
		}
	}

	// Single column left on a very narrow terminal: just clamp it.
	if shown == 1 && out[0] > total {
		out[0] = max(total, 1)
	}
	return out
}

// cell truncates or pads s (ANSI-aware) to exactly w display columns.
func cell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if lipgloss.Width(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

func joinCells(cells []string, ws []int) string {
	var b strings.Builder
	for i, c := range cells {
		if i >= len(ws) || ws[i] < 0 {
			break
		}
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(cell(c, ws[i]))
	}
	return b.String()
}

// renderTable draws a header plus a scrolled window of rows sized to height
// lines, keeping the cursor visible.
func renderTable(cols []col, rows []row, cursor, offset, width, height int) string {
	ws := widths(cols, width)
	titles := make([]string, len(cols))
	for i, c := range cols {
		titles[i] = strings.ToUpper(c.title)
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render(joinCells(titles, ws)))
	b.WriteString("\n")

	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("  nothing here"))
		return b.String()
	}
	end := min(offset+height, len(rows))
	for i := offset; i < end; i++ {
		line := joinCells(rows[i].cells, ws)
		if i == cursor {
			b.WriteString(rowSelStyle.Render("▌" + line))
		} else {
			b.WriteString(rowStyle.Render(" " + line))
		}
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// clampOffset scrolls the window so cursor stays inside it.
func clampOffset(cursor, offset, height, n int) int {
	if height <= 0 {
		return 0
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+height {
		offset = cursor - height + 1
	}
	if offset > n-height {
		offset = n - height
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}
