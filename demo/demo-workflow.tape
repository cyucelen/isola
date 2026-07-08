# isola Worktree Isolation Demo
# Spin up an isolated environment for a worktree: its own services (ports +
# *.localhost URLs) and its own database, cloned from a template.

Output demo-workflow.gif

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

# Create a worktree for a branch and enter it
Type "git worktree add ../demo-feature feature/new-api"
Enter
Sleep 2s
Type "cd ../demo-feature"
Enter
Sleep 700ms

# Bring up its environment — services get ports, and a database is cloned
Type "isola up"
Enter
Sleep 3s

# Ports, status, and *.localhost URLs
Type "isola ls"
Enter
Sleep 3s

# This worktree's own database, cloned from the myapp_dev template
Type "isola accessory ls"
Enter
Sleep 3.5s

# Tear down
Type "isola down"
Enter
Sleep 1.5s
