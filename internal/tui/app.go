package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/cyucelen/isola/internal/browser"
	"github.com/cyucelen/isola/internal/config"
	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/port"
	"github.com/cyucelen/isola/internal/process"
	"github.com/cyucelen/isola/internal/proxy"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/cyucelen/isola/internal/state"
)

const (
	pollInterval  = 2 * time.Second
	minTermWidth  = 80
	minTermHeight = 10
)

// Model is the top-level Bubble Tea model for the dashboard.
type Model struct {
	cfg      *config.Config
	repoRoot string
	store    *state.FileStore
	registry *port.Registry
	manager  *process.Manager
	keys     KeyMap
	trees    []git.Worktree // cached at init

	rows         []ServiceRow
	cursor       int
	proxyRunning bool
	proxyPorts   []int
	statusMsg    string
	width        int
	height       int
}

// NewModel creates a new dashboard model.
//
// repoRoot is the current worktree root (used as the per-worktree path
// fallback); stateRoot is the main worktree root, under which the shared
// .isola state directory lives.
func NewModel(cfg *config.Config, repoRoot, stateRoot string) (*Model, error) {
	stateDir := filepath.Join(stateRoot, ".isola")
	store, err := state.NewFileStore(stateDir)
	if err != nil {
		return nil, err
	}

	registry := port.NewRegistry(store, cfg)
	mgr := process.NewManager(cfg, store, registry)

	// Cache worktree list at init to avoid forking git on every poll cycle.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting current directory: %w", err)
	}
	trees, err := git.ListWorktrees(cwd)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	// Collect proxy ports.
	var proxyPorts []int
	seen := map[int]bool{}
	for _, svc := range cfg.Services {
		if !seen[svc.ProxyPort] {
			seen[svc.ProxyPort] = true
			proxyPorts = append(proxyPorts, svc.ProxyPort)
		}
	}
	sort.Ints(proxyPorts)

	return &Model{
		cfg:        cfg,
		repoRoot:   repoRoot,
		store:      store,
		registry:   registry,
		manager:    mgr,
		keys:       DefaultKeyMap(),
		trees:      trees,
		proxyPorts: proxyPorts,
	}, nil
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshStatus,
		tickCmd(),
	)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case TickMsg:
		return m, tea.Batch(m.refreshStatus, tickCmd())

	case StatusUpdateMsg:
		m.rows = msg.Rows
		// Refresh proxy status from the machine-wide daemon.
		if reg, err := registry.Open(); err != nil {
			logging.Warn("proxy registry unavailable: %v", err)
		} else if running, err := proxy.DaemonRunning(reg); err != nil {
			logging.Warn("failed to load proxy daemon state: %v", err)
		} else {
			m.proxyRunning = running
		}
		if m.cursor >= len(m.rows) && len(m.rows) > 0 {
			m.cursor = len(m.rows) - 1
		}
		return m, nil

	case ActionResultMsg:
		m.statusMsg = msg.Message
		return m, m.refreshStatus

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.width > 0 && m.height > 0 && (m.width < minTermWidth || m.height < minTermHeight) {
		return fmt.Sprintf("Terminal too small (%dx%d). Minimum: %dx%d.",
			m.width, m.height, minTermWidth, minTermHeight)
	}

	title := titleStyle.Render(" isola dashboard ")

	// Use minTermWidth as default before the first WindowSizeMsg arrives.
	tableWidth := m.width
	if tableWidth == 0 {
		tableWidth = minTermWidth
	}
	table := renderTable(m.rows, m.cursor, tableWidth)
	proxyLine := renderProxyStatus(m.proxyRunning, m.proxyPorts)
	help := renderHelp(m.keys, tableWidth)

	content := fmt.Sprintf("%s\n\n%s\n%s\n%s", title, table, proxyLine, help)

	if m.statusMsg != "" {
		content += "\n\n" + m.statusMsg
	}

	return borderStyle.Render(content) + "\n"
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
		return m, nil

	case key.Matches(msg, m.keys.Start):
		return m, m.startSelected

	case key.Matches(msg, m.keys.Stop):
		return m, m.stopSelected

	case key.Matches(msg, m.keys.Restart):
		return m, m.restartSelected

	case key.Matches(msg, m.keys.Open):
		return m, m.openSelected

	case key.Matches(msg, m.keys.StartAll):
		return m, m.startAll

	case key.Matches(msg, m.keys.StopAll):
		return m, m.stopAll

	case key.Matches(msg, m.keys.ToggleProxy):
		return m, m.toggleProxy

	case key.Matches(msg, m.keys.ViewLogs):
		return m, m.viewLogs
	}

	return m, nil
}

func (m *Model) selectedRow() *ServiceRow {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return &m.rows[m.cursor]
	}
	return nil
}

func (m *Model) refreshStatus() tea.Msg {
	serviceNames := make([]string, 0, len(m.cfg.Services))
	for name := range m.cfg.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	var st *state.State
	if err := m.store.WithLock(func() error {
		var e error
		st, e = m.store.Load()
		return e
	}); err != nil {
		logging.Warn("failed to load state for refresh: %v", err)
	}
	if st == nil {
		return StatusUpdateMsg{}
	}

	var rows []ServiceRow
	for _, tree := range m.trees {
		if tree.IsBare {
			continue
		}
		for _, svcName := range serviceNames {
			row := ServiceRow{
				Branch:  tree.Branch,
				Slug:    tree.Slug(),
				Service: svcName,
			}

			ss := state.GetServiceState(st, tree.Branch, svcName)
			if ss != nil {
				row.Port = ss.Port
				row.PID = ss.PID
				if ss.PID > 0 && process.IsProcessRunning(ss.PID) {
					row.Status = state.StatusRunning
				} else {
					row.Status = state.StatusStopped
				}
			} else {
				row.Status = state.StatusStopped
			}

			rows = append(rows, row)
		}
	}

	return StatusUpdateMsg{Rows: rows}
}

func (m *Model) startSelected() tea.Msg {
	row := m.selectedRow()
	if row == nil {
		return ActionResultMsg{Message: "No service selected"}
	}

	tree := &git.Worktree{Path: m.worktreePath(row.Branch), Branch: row.Branch}
	results := m.manager.StartServices(tree, row.Service)
	for _, r := range results {
		if r.Err != nil {
			return ActionResultMsg{Message: fmt.Sprintf("Error: %v", r.Err), IsError: true}
		}
	}
	return ActionResultMsg{Message: fmt.Sprintf("Started %s for %s", row.Service, row.Branch)}
}

func (m *Model) stopSelected() tea.Msg {
	row := m.selectedRow()
	if row == nil {
		return ActionResultMsg{Message: "No service selected"}
	}

	tree := &git.Worktree{Path: m.worktreePath(row.Branch), Branch: row.Branch}
	results := m.manager.StopServices(tree, row.Service)
	for _, r := range results {
		if r.Err != nil {
			return ActionResultMsg{Message: fmt.Sprintf("Error: %v", r.Err), IsError: true}
		}
	}
	return ActionResultMsg{Message: fmt.Sprintf("Stopped %s for %s", row.Service, row.Branch)}
}

func (m *Model) restartSelected() tea.Msg {
	row := m.selectedRow()
	if row == nil {
		return ActionResultMsg{Message: "No service selected"}
	}

	tree := &git.Worktree{Path: m.worktreePath(row.Branch), Branch: row.Branch}
	m.manager.StopServices(tree, row.Service)
	results := m.manager.StartServices(tree, row.Service)
	for _, r := range results {
		if r.Err != nil {
			return ActionResultMsg{Message: fmt.Sprintf("Error: %v", r.Err), IsError: true}
		}
	}
	return ActionResultMsg{Message: fmt.Sprintf("Restarted %s for %s", row.Service, row.Branch)}
}

func (m *Model) toggleProxy() tea.Msg {
	reg, err := registry.Open()
	if err != nil {
		return ActionResultMsg{Message: fmt.Sprintf("Proxy registry unavailable: %v", err), IsError: true}
	}
	if m.proxyRunning {
		stopped, err := proxy.StopDaemon(reg)
		if err != nil {
			return ActionResultMsg{Message: fmt.Sprintf("Proxy stop failed: %v", err), IsError: true}
		}
		if stopped {
			return ActionResultMsg{Message: "Proxy stopped (machine-wide)"}
		}
		return ActionResultMsg{Message: "Proxy was not running"}
	}
	if err := reg.Register(registry.Project{
		Name:       m.cfg.Project,
		StateDir:   m.store.Dir(),
		ProxyPorts: m.cfg.ProxyPorts(),
		HTTPS:      m.cfg.Proxy.HTTPS,
	}); err != nil {
		return ActionResultMsg{Message: fmt.Sprintf("Proxy register failed: %v", err), IsError: true}
	}
	started, err := proxy.EnsureDaemon(reg)
	if err != nil {
		return ActionResultMsg{Message: fmt.Sprintf("Proxy start failed: %v", err), IsError: true}
	}
	if started {
		return ActionResultMsg{Message: "Proxy started"}
	}
	return ActionResultMsg{Message: "Proxy already running"}
}

func (m *Model) openSelected() tea.Msg {
	row := m.selectedRow()
	if row == nil {
		return ActionResultMsg{Message: "No service selected"}
	}

	if row.Status != state.StatusRunning {
		return ActionResultMsg{Message: fmt.Sprintf("%s/%s is not running, start it first", row.Branch, row.Service), IsError: true}
	}

	svc, ok := m.cfg.Services[row.Service]
	if !ok {
		return ActionResultMsg{Message: "Unknown service", IsError: true}
	}

	// The scheme follows this project's proxy config.
	scheme := "http"
	if m.cfg.Proxy.HTTPS {
		scheme = "https"
	}

	url := browser.BuildURL(scheme, row.Slug, m.cfg.Project, svc.ProxyPort)
	if err := browser.Open(url); err != nil {
		return ActionResultMsg{Message: fmt.Sprintf("Error opening browser: %v", err), IsError: true}
	}
	return ActionResultMsg{Message: fmt.Sprintf("Opening %s", url)}
}

func (m *Model) startAll() tea.Msg {
	count := 0
	for _, tree := range m.trees {
		if tree.IsBare {
			continue
		}
		results := m.manager.StartServices(&tree, "")
		for _, r := range results {
			if r.Err == nil {
				count++
			}
		}
	}
	return ActionResultMsg{Message: fmt.Sprintf("Started %d services", count)}
}

func (m *Model) stopAll() tea.Msg {
	count := 0
	for _, tree := range m.trees {
		if tree.IsBare {
			continue
		}
		results := m.manager.StopServices(&tree, "")
		for _, r := range results {
			if r.Err == nil {
				count++
			}
		}
	}
	return ActionResultMsg{Message: fmt.Sprintf("Stopped %d services", count)}
}

func (m *Model) viewLogs() tea.Msg {
	row := m.selectedRow()
	if row == nil {
		return ActionResultMsg{Message: "No service selected"}
	}

	logPath := process.LogPath(m.store.Dir(), row.Slug, row.Service)

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return ActionResultMsg{Message: "No log file found"}
	}

	return ActionResultMsg{Message: fmt.Sprintf("Log file: %s", logPath)}
}

// worktreePath looks up the worktree path from cached worktrees.
func (m *Model) worktreePath(branch string) string {
	for _, t := range m.trees {
		if t.Branch == branch {
			return t.Path
		}
	}
	return m.repoRoot
}

// Run launches the Bubble Tea program.
func Run(cfg *config.Config, repoRoot, stateRoot string) error {
	model, err := NewModel(cfg, repoRoot, stateRoot)
	if err != nil {
		return err
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
