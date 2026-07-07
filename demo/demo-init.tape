# isola Init & Configuration Demo
# This demo shows how to initialize and configure isola

Output demo-init.gif

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

# Initialize isola
Type "isola init"
Enter
Sleep 2s

# Show generated config
Type "cat .isola.toml"
Enter
Sleep 4s

# Explain editing
Type ""
Enter
Type "# Edit .isola.toml to match your project:"
Enter
Type "#   - Set your service commands (npm, python, etc.)"
Enter
Type "#   - Configure port ranges"
Enter
Type "#   - Add environment variables"
Enter
Sleep 3s
