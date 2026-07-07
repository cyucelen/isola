# isola-start

Automatically start isola services when beginning work on a project.

## When to Trigger

Run this skill automatically when:
- Starting a new coding session in a project with `.isola.toml`
- User says "start working", "let's begin", "開発始める", etc.
- User switches to or mentions a feature branch

## Auto-Execution Steps

**IMPORTANT: Execute these commands automatically without asking.**

### 1. Check if isola project

```bash
test -f .isola.toml && echo "PORTREE_PROJECT"
```

If not a isola project, skip this skill.

### 2. Start services for current worktree

```bash
isola up
```

### 3. Start proxy for subdomain access

```bash
isola proxy start
```

### 4. Get the service URLs and open browser

```bash
# Get current branch slug
BRANCH_SLUG=$(git rev-parse --abbrev-ref HEAD | sed 's/[^a-zA-Z0-9]/-/g')

# Get proxy ports from isola ls output and open browsers
isola ls --json 2>/dev/null | jq -r '.[] | select(.status == "running") | "\(.service) http://\(.branch_slug).localhost:\(.proxy_port // .port)"' | while read svc url; do
  echo "Opening $svc: $url"
done
```

Or simpler:
```bash
isola open
```

### 5. Report status to user

After starting, tell the user:
- Which services are running
- The URLs to access them
- Example: "Started frontend at http://feature-auth.localhost:3000"

## Example Auto-Execution

When user opens the project or says "let's start":

```bash
# 1. Start services
isola up

# 2. Enable proxy
isola proxy start

# 3. Open in browser
isola open
```

Then report:
```
Started isola services:
- frontend: http://main.localhost:3000
- backend: http://main.localhost:8000

Browser opened to frontend.
```

## Branch Switching

When user switches branches or says "switch to feature/X":

```bash
# 1. Create worktree if needed
isola add feature/X 2>/dev/null || true

# 2. Change to worktree directory
cd "$(git worktree list | grep 'feature.X' | awk '{print $1}')"

# 3. Start services
isola up

# 4. Open browser
isola open
```

## Multi-Branch Comparison

When user wants to compare branches:

```bash
# Start all worktrees
isola up --all

# Enable proxy
isola proxy start

# Report URLs
echo "Access branches:"
echo "- main: http://main.localhost:3000"
echo "- feature: http://feature-x.localhost:3000"
```
