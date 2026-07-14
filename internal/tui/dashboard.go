package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cyucelen/isola/internal/state"
)

// Fixed column widths for SERVICE, PORT, STATUS, PID.
const (
	colServiceWidth = 12
	colPortWidth    = 8
	colStatusWidth  = 14
	colPIDWidth     = 10
	colSeparators   = 4 * 2 // 4 separators × 2 chars ("  ")
	colCursorPrefix = 2     // "▸ " or "  "
	colMinWorktree  = 18
	// borderOverhead accounts for the panel's RoundedBorder (1 char each side)
	// plus Padding(1,2) (2 chars each side) = ~6. Update if borderStyle in
	// styles.go changes.
	borderOverhead = 6
)

// fixedColumnsWidth is the sum of all non-WORKTREE columns plus separators and cursor.
const fixedColumnsWidth = colServiceWidth + colPortWidth + colStatusWidth + colPIDWidth + colSeparators + colCursorPrefix

// worktreeColumnWidth computes the dynamic WORKTREE column width.
func worktreeColumnWidth(termWidth int) int {
	available := termWidth - fixedColumnsWidth - borderOverhead
	if available < colMinWorktree {
		return colMinWorktree
	}
	return available
}

// renderRow renders one table row: each value laid into its column width with
// base, joined by the two-space separator. Header and data rows share it,
// differing only in base style and values.
func renderRow(widths []int, values []string, base lipgloss.Style) string {
	cells := make([]string, len(values))
	for i, v := range values {
		cells[i] = base.Width(widths[i]).Render(v)
	}
	return strings.Join(cells, "  ")
}

// renderTable renders the dashboard table with the given rows and cursor position.
func renderTable(rows []ServiceRow, cursor int, termWidth int) string {
	wtWidth := worktreeColumnWidth(termWidth)
	widths := []int{wtWidth, colServiceWidth, colPortWidth, colStatusWidth, colPIDWidth}

	var b strings.Builder

	// Header. Leading spaces align with the cursor prefix on data rows.
	header := "  " + renderRow(widths,
		[]string{"WORKTREE", "SERVICE", "PORT", "STATUS", "PID"},
		lipgloss.NewStyle().Bold(true).Foreground(colorWhite))
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	// Rows
	for i, row := range rows {
		portStr := "—"
		if row.Port > 0 {
			portStr = fmt.Sprintf("%d", row.Port)
		}

		statusStr := statusStopped
		if row.Status == state.StatusRunning {
			statusStr = statusRunning
		}

		pidStr := "—"
		if row.PID > 0 {
			pidStr = fmt.Sprintf("%d", row.PID)
		}

		line := renderRow(widths,
			[]string{row.Branch, row.Service, portStr, statusStr, pidStr},
			lipgloss.NewStyle())

		if i == cursor {
			// Prepend cursor indicator
			line = "▸ " + line
			b.WriteString(selectedRowStyle.Render(line))
		} else {
			line = "  " + line
			b.WriteString(rowStyle.Render(line))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// renderProxyStatus renders the proxy status line.
func renderProxyStatus(running bool, ports []int) string {
	if running {
		portStrs := make([]string, len(ports))
		for i, p := range ports {
			portStrs[i] = fmt.Sprintf(":%d", p)
		}
		return proxyRunningStyle.Render(
			fmt.Sprintf("Proxy: ● running (%s)", strings.Join(portStrs, ", ")))
	}
	return proxyStoppedStyle.Render("Proxy: ○ stopped")
}

// renderHelp renders the key binding help bar with automatic wrapping.
func renderHelp(keys KeyMap, width int) string {
	items := []string{
		"[s] start", "[x] stop", "[r] restart", "[o] open", "[l] logs",
		"[a] all start", "[X] all stop", "[p] proxy", "[q] quit",
	}

	maxWidth := width - borderOverhead
	if maxWidth < 40 {
		maxWidth = 40
	}

	var lines []string
	var currentLine []string
	currentLen := 0
	separator := "  "

	for _, item := range items {
		itemLen := len(item)
		newLen := currentLen + itemLen
		if len(currentLine) > 0 {
			newLen += len(separator)
		}

		if newLen > maxWidth && len(currentLine) > 0 {
			lines = append(lines, helpStyle.Render(strings.Join(currentLine, separator)))
			currentLine = []string{item}
			currentLen = itemLen
		} else {
			currentLine = append(currentLine, item)
			currentLen = newLen
		}
	}

	if len(currentLine) > 0 {
		lines = append(lines, helpStyle.Render(strings.Join(currentLine, separator)))
	}

	return strings.Join(lines, "\n")
}
