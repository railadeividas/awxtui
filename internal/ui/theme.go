package ui

import "github.com/charmbracelet/lipgloss"

// A restrained palette: one accent, one muted tone, and semantic status colours.
// Adaptive so it stays legible on light and dark terminals.
var (
	accent  = lipgloss.AdaptiveColor{Light: "#6435C9", Dark: "#A78BFA"}
	fg      = lipgloss.AdaptiveColor{Light: "#1F2430", Dark: "#E6E6EB"}
	muted   = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8A8F98"}
	faint   = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#5C626B"}
	ok      = lipgloss.AdaptiveColor{Light: "#127A3E", Dark: "#4ADE80"}
	warn    = lipgloss.AdaptiveColor{Light: "#A15C00", Dark: "#FBBF24"}
	danger  = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	surface = lipgloss.AdaptiveColor{Light: "#EDEAF7", Dark: "#2A2438"}
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	metaStyle  = lipgloss.NewStyle().Foreground(muted)

	tabStyle       = lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
	tabActiveStyle = lipgloss.NewStyle().Foreground(accent).Bold(true).
			Background(surface).Padding(0, 1)

	headerStyle = lipgloss.NewStyle().Foreground(faint).Bold(true)
	rowStyle    = lipgloss.NewStyle().Foreground(fg)
	rowSelStyle = lipgloss.NewStyle().Foreground(fg).Background(surface).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(faint)

	ruleStyle = lipgloss.NewStyle().Foreground(faint)

	helpKeyStyle  = lipgloss.NewStyle().Foreground(accent)
	helpDescStyle = lipgloss.NewStyle().Foreground(muted)
	// helpOnStyle marks a key whose action is already in force — unpin on a
	// pinned row, show on a narrowed list — so the state of the view can be
	// read off the legend without inspecting the rows.
	helpOnStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)

	// pinStyle marks a run kept on purpose; mineStyle marks one you started.
	// Both have to stand out in a list where every other row is dim.
	pinStyle  = lipgloss.NewStyle().Foreground(warn).Bold(true)
	mineStyle = lipgloss.NewStyle().Foreground(accent)

	errStyle  = lipgloss.NewStyle().Foreground(danger)
	okStyle   = lipgloss.NewStyle().Foreground(ok)
	warnStyle = lipgloss.NewStyle().Foreground(warn)

	inputStyle = lipgloss.NewStyle().Foreground(fg)

	// search hits inside job output
	matchStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#1F2430")).Background(lipgloss.Color("#FBBF24"))
	matchCurrentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#1F2430")).Background(lipgloss.Color("#4ADE80")).Bold(true)

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(1, 2)
)

// statusBadge renders an AWX job/project status with a glyph and colour.
func statusBadge(status string) string {
	glyph, style := "•", dimStyle
	switch status {
	case "successful", "ok":
		glyph, style = "✓", okStyle
	case "failed", "error", "canceled":
		glyph, style = "✗", errStyle
	case "running":
		glyph, style = "▸", warnStyle
	case "pending", "waiting", "new":
		glyph, style = "◦", warnStyle
	case "":
		return dimStyle.Render("— never run")
	}
	return style.Render(glyph + " " + status)
}
