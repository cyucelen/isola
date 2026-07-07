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

# Check status
Type "isola ls"
Enter
Sleep 2.5s

# Show accessible URLs
Type "echo ''"
Enter
Type "echo 'Access URLs:'"
Enter
Sleep 300ms
Type "echo '  http://main.localhost:3000  → frontend'"
Enter
Sleep 300ms
Type "echo '  http://main.localhost:8000  → backend'"
Enter
Sleep 2s

# Open in browser
Type "isola open"
Enter
Sleep 2s

# Stop services
Type "isola down"
Enter
Sleep 2s
