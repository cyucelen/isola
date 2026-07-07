# AI Agent Instructions for Isola

## IMPORTANT: Auto-Execution Behavior

This project uses **isola** for multi-branch development. AI agents should **automatically** manage services without waiting for explicit commands.

### On Session Start

**AUTOMATICALLY execute when beginning work:**

```bash
# Check if isola project
if [ -f .isola.toml ]; then
  # Start services
  isola up

  # Enable subdomain routing
  isola proxy start

  # Open browser
  isola open
fi
```

Then report to user:
```
Started isola services:
- frontend: http://main.localhost:3000
- backend: http://main.localhost:8000
Browser opened.
```

### On Branch Switch

**AUTOMATICALLY execute when user switches to or mentions a branch:**

```bash
# Create worktree if needed
isola add <branch> 2>/dev/null || true

# Navigate to worktree (if not already there)
cd "$(git worktree list | grep '<branch>' | head -1 | awk '{print $1}')" 2>/dev/null || true

# Start services
isola up

# Open browser
isola open
```

### On "Compare Branches" Request

**AUTOMATICALLY execute:**

```bash
isola up --all
isola proxy start
```

Report URLs:
```
Branches running:
- main: http://main.localhost:3000
- feature-x: http://feature-x.localhost:3000
```

### On Session End / Cleanup

**When user says "done", "finished", "終わり", etc.:**

```bash
isola down --all
```

---

## Quick Reference

### Commands
```bash
isola up              # Start current branch
isola up --all        # Start all branches
isola down            # Stop current
isola down --all      # Stop all
isola proxy start     # Enable subdomain routing
isola open [service]  # Open browser
isola ls              # Show status
isola dash            # Interactive dashboard
isola add <branch>    # Create worktree
isola remove <branch> # Remove worktree
```

### URL Pattern
```
http://<branch-slug>.localhost:<proxy-port>
```
Examples:
- `http://main.localhost:3000`
- `http://feature-auth.localhost:3000`

### Environment Variables (injected into services)
| Variable | Description |
|----------|-------------|
| `PORT` | Assigned port |
| `ISOLA_BRANCH` | Branch name |
| `ISOLA_BRANCH_SLUG` | URL-safe branch |
| `ISOLA_{SERVICE}_PORT` | Other service's port |
| `ISOLA_{SERVICE}_URL` | Other service's proxy URL |

### Configuration (.isola.toml)
```toml
[services.frontend]
command = "npm run dev"
port_env = "PORT"
port_range = [3100, 3199]

[services.backend]
command = "go run ."
port_range = [8100, 8199]
```

---

## Troubleshooting

| Issue | Solution |
|-------|----------|
| Service won't start | `isola doctor` |
| Port in use | `lsof -i :<port>` then kill process |
| Proxy not working | `isola proxy start` |
| Logs | `~/.isola/logs/<branch>.<service>.log` |

---

## Installation

```bash
# macOS
brew install cyucelen/tap/isola

# Other
# Download from https://github.com/cyucelen/isola/releases
```
