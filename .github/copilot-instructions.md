# GitHub Copilot Instructions for Isola

## AUTO-EXECUTION REQUIRED

This project uses **isola** for multi-branch development. Copilot should **automatically** start services when working on this project.

### When Starting Work

**Execute immediately:**
```bash
isola up && isola proxy start && isola open
```

Report the running services and URLs to the user.

### When Switching Branches

**Execute automatically:**
```bash
isola add <branch> 2>/dev/null || true
isola up
isola open
```

### When Comparing Branches

**Execute:**
```bash
isola up --all
isola proxy start
```

Report: `http://main.localhost:3000` vs `http://feature-x.localhost:3000`

### When Done

**Execute:**
```bash
isola down --all
```

---

## Quick Reference

### Essential Commands
| Command | Action |
|---------|--------|
| `isola up` | Start current branch |
| `isola up --all` | Start all branches |
| `isola down` | Stop current |
| `isola proxy start` | Enable subdomain routing |
| `isola open` | Open browser |
| `isola ls` | Show status |

### URL Format
```
http://<branch-slug>.localhost:<port>
```

### Environment Variables
| Variable | Description |
|----------|-------------|
| `PORT` | Assigned port |
| `ISOLA_BRANCH` | Branch name |
| `ISOLA_BRANCH_SLUG` | URL-safe branch |
| `ISOLA_{SERVICE}_PORT` | Other service's port |

### Configuration (.isola.toml)
```toml
[services.frontend]
command = "npm run dev"
port_env = "PORT"

[services.backend]
command = "go run ."
port_range = [8100, 8199]
```

### Troubleshooting
- Won't start: `isola doctor`
- Port conflict: `lsof -i :<port>`
- Logs: `~/.isola/logs/`
