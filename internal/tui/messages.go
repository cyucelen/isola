package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TickMsg triggers periodic state refresh.
type TickMsg struct{}

// StatusUpdateMsg carries refreshed service status data.
type StatusUpdateMsg struct {
	Rows []ServiceRow
}

// ServiceRow represents a single row in the dashboard table.
type ServiceRow struct {
	Branch string
	// HostLabel is the worktree's DNS label, for its proxy URL and log path.
	HostLabel string
	Service   string
	Port      int
	Status    string // state.StatusRunning or state.StatusStopped
	PID       int
}

// ActionResultMsg carries the result of a user action (start/stop/restart).
type ActionResultMsg struct {
	Message string
	IsError bool
}

// tickCmd returns a command that sends a TickMsg after pollInterval.
func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(_ time.Time) tea.Msg {
		return TickMsg{}
	})
}
