# isola Installation Demo
# This demo shows how to install isola (display only, no execution)

Output demo-install.gif

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 600
Set Theme "Catppuccin Mocha"
Set Padding 20
Set Framerate 30
Set PlaybackSpeed 0.75

# Option 1: Homebrew
Type "echo '# Option 1: Homebrew'"
Enter
Sleep 500ms
Type "echo 'brew install cyucelen/tap/isola'"
Enter
Sleep 2s

# Option 2: Go install
Type "echo ''"
Enter
Type "echo '# Option 2: Go install'"
Enter
Sleep 500ms
Type "echo 'go install github.com/cyucelen/isola@latest'"
Enter
Sleep 2s

# Option 3: Build from source
Type "echo ''"
Enter
Type "echo '# Option 3: Build from source'"
Enter
Sleep 500ms
Type "echo 'git clone https://github.com/cyucelen/isola.git'"
Enter
Type "echo 'cd isola && make build'"
Enter
Sleep 2s

# Verify (this one actually runs)
Type "echo ''"
Enter
Type "echo '# Verify installation'"
Enter
Sleep 500ms
Type "isola version"
Enter
Sleep 2s
