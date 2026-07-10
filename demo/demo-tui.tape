# isola TUI Dashboard Demo
# This demo shows the interactive terminal dashboard with multiple worktrees

Output demo-tui.gif

Require isola
Require git

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 600
Set Theme { "background": "#0c1117", "foreground": "#c9d1d9", "selection": "#264f78", "cursor": "#c9d1d9", "black": "#484f58", "brightBlack": "#6e7681", "red": "#ff7b72", "brightRed": "#ffa198", "green": "#3fb950", "brightGreen": "#56d364", "yellow": "#d29922", "brightYellow": "#e3b341", "blue": "#58a6ff", "brightBlue": "#79c0ff", "magenta": "#bc8cff", "brightMagenta": "#d2a8ff", "cyan": "#39c5cf", "brightCyan": "#56d4dd", "white": "#b1bac4", "brightWhite": "#f0f6fc" }
Set Padding 20
Set Framerate 30
Set PlaybackSpeed 1.0

# Setup: create a second worktree and start each worktree's services
Hide
Type "git worktree add ../demo-feature feature/new-api 2>/dev/null || true"
Enter
Sleep 500ms
Type "isola up"
Enter
Sleep 2s
Type "cd ../demo-feature"
Enter
Sleep 300ms
Type "isola up"
Enter
Sleep 2s
Type "cd ../isola-demo"
Enter
Sleep 300ms
Show

# Launch dashboard
Type "isola dash"
Enter
Sleep 2s

# Navigate through services (4 services: 2 worktrees x 2 services)
Type "j"
Sleep 500ms
Type "j"
Sleep 500ms
Type "j"
Sleep 500ms

# Go back up
Type "k"
Sleep 500ms
Type "k"
Sleep 500ms

# Stop a service
Type "x"
Sleep 1.5s

# Start it again
Type "s"
Sleep 1.5s

# Restart
Type "r"
Sleep 1.5s

# Stop all
Type "X"
Sleep 1.5s

# Start all
Type "a"
Sleep 1.5s

# Hold on the dashboard so viewers can read it
Sleep 30s

# Quit
Type "q"
Sleep 1s

# Cleanup
Hide
Type "isola down"
Enter
Sleep 500ms
Type "cd ../demo-feature"
Enter
Type "isola down"
Enter
Sleep 500ms
Type "cd ../isola-demo"
Enter
Type "git worktree remove ../demo-feature --force 2>/dev/null || true"
Enter
Show
Sleep 500ms
