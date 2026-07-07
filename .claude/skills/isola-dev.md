# isola-dev

Development workflow with isola for multi-branch development.

## When to Use

- User wants to work on a feature branch while keeping main running
- User asks how to start/stop services
- User wants to compare branches side-by-side
- User needs to manage multiple worktrees

## Commands Reference

### Worktree Management

```bash
# Create worktree for a branch
isola add <branch>

# Create worktree for new branch (from current HEAD)
isola add -b <new-branch>

# List all worktrees and their services
isola ls

# Remove a worktree
isola remove <branch>
```

### Service Management

```bash
# Start services for current worktree
isola up

# Start all services across all worktrees
isola up --all

# Stop services for current worktree
isola down

# Stop all services
isola down --all

# Restart services
isola restart
```

### Proxy & Dashboard

```bash
# Start reverse proxy (enables subdomain routing)
isola proxy start

# Stop proxy
isola proxy stop

# Open interactive dashboard
isola dash

# Dashboard keybindings:
#   j/k or ↑/↓  Navigate services
#   s           Start selected service
#   x           Stop selected service
#   r           Restart selected service
#   o           Open in browser
#   a           Start all services
#   X           Stop all services
#   l           View logs
#   p           Toggle proxy
#   q           Quit dashboard
```

### Browser Access

```bash
# Open service in browser
isola open [service]

# With proxy running, access via subdomain:
# http://<branch-slug>.localhost:<proxy-port>
# Example: http://feature-auth.localhost:3000
```

## Common Workflows

### 1. Start working on a feature branch

```bash
# Create worktree and start services
isola add feature/new-feature
cd ../feature-new-feature  # or use the path shown
isola up

# Or stay in main and start everything
isola add feature/new-feature
isola up --all
```

### 2. Compare two branches side-by-side

```bash
# Start proxy for subdomain routing
isola proxy start

# Start both branches
isola up --all

# Access:
# - main: http://main.localhost:3000
# - feature: http://feature-new-feature.localhost:3000
```

### 3. Quick branch switching

```bash
# Use dashboard for visual management
isola dash

# Or use CLI
isola ls                    # See all branches/services
isola down                  # Stop current
cd ../other-branch
isola up                    # Start other
```

### 4. Clean up after PR merge

```bash
isola down --all            # Stop everything
isola remove feature/done   # Remove worktree
```

## Troubleshooting

### Service won't start
```bash
isola doctor                # Check configuration
isola ls                    # Check port conflicts
```

### Port already in use
```bash
# Check what's using the port
lsof -i :<port>

# isola tracks orphan processes - restart should work
isola restart
```

### Proxy not routing correctly
```bash
# Ensure proxy is running
isola proxy start

# Check /etc/hosts has localhost entries (macOS)
# Or use *.localhost which resolves automatically
```
