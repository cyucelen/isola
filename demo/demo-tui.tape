# isola TUI Dashboard Demo
# This demo shows the interactive terminal dashboard with multiple worktrees

Output demo-tui.gif

Require isola
Require git

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 600
Set Theme "Catppuccin Mocha"
Set Padding 20
Set Framerate 30
Set PlaybackSpeed 0.75

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
