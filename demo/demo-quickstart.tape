# isola Quick Start Demo
# This demo shows the basic workflow: config → up → ls → URLs → open

Output demo-quickstart.gif

Require isola

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 600
Set Theme "Catppuccin Mocha"
Set Padding 20
Set Framerate 30
Set PlaybackSpeed 0.75

# Show config
Type "cat .isola.toml"
Enter
Sleep 3s

# Start services
Type "isola up"
Enter
Sleep 2.5s

# Check status — ports, status, and *.localhost URLs
Type "isola ls"
Enter
Sleep 3.5s

# The worktree's own database, cloned from the template
Type "isola accessory ls"
Enter
Sleep 3.5s

# Stop services
Type "isola down"
Enter
Sleep 2s
