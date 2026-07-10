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
Set Theme { "background": "#0c1117", "foreground": "#c9d1d9", "selection": "#264f78", "cursor": "#c9d1d9", "black": "#484f58", "brightBlack": "#6e7681", "red": "#ff7b72", "brightRed": "#ffa198", "green": "#3fb950", "brightGreen": "#56d364", "yellow": "#d29922", "brightYellow": "#e3b341", "blue": "#58a6ff", "brightBlue": "#79c0ff", "magenta": "#bc8cff", "brightMagenta": "#d2a8ff", "cyan": "#39c5cf", "brightCyan": "#56d4dd", "white": "#b1bac4", "brightWhite": "#f0f6fc" }
Set Padding 20
Set Framerate 30
Set PlaybackSpeed 1.0
Set TypingSpeed 40ms

# Create a worktree for a branch and enter it
Type "git worktree add ../demo-feature feature/new-api"
Enter
Sleep 800ms
Type "cd ../demo-feature"
Enter
Sleep 400ms

# Bring up its environment — services get ports, and a database is cloned
Type "isola up"
Enter
Sleep 1800ms

# Ports, status, and *.localhost URLs
Type "isola ls"
Enter
Sleep 1800ms

# This worktree's own database, cloned from the myapp_dev template
Type "isola accessory ls"
Enter
Sleep 2200ms

# Tear down
Type "isola down"
Enter
Sleep 1s

# Hold on the final frame so viewers can read it
Sleep 30s
