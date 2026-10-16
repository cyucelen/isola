# isola Demo GIFs

This directory contains VHS tape files for generating demo GIFs for the README.

## Run the database isolation proof

See what happens when one worktree deletes its data while another is running:

With isola already installed, run this from the repository root (requires git,
Python 3, and running Docker):

```bash
python3 demo/prove-isolation.py
```

Or build isola from source first:

```bash
# From the isola repository root; requires Go, git, Python 3, and running Docker.
go build -o isola .
python3 demo/prove-isolation.py --isola ./isola
```

The script creates a temporary git repository, two worktrees, and a disposable
Postgres 16 container. Each worktree runs an HTTP service using its injected
`PORT` and `DATABASE_URL`. Both begin with three todos. The script deletes the
feature worktree's todos, reads both services, and verifies that main still has
three. It also verifies that `isola destroy` drops the worktree databases and
preserves the template.

The container binds a randomly allocated loopback port. The script cleans up its
services, container, and temporary files when it exits normally or raises an
exception. The demo disables isola's proxy and tests the services directly.
Docker and the Python service's use of `docker exec` are conveniences for this
self-contained fixture; isola itself connects to your existing Postgres server.

The final checks print `PASS` only after reading the actual services and database.

## Prerequisites

- [VHS](https://github.com/charmbracelet/vhs) - Terminal recorder
- Go (for building isola)
- Python 3 (for mock servers)
- Docker (for the throwaway Postgres that backs the per-worktree database demo)

```bash
# Install VHS
brew install vhs
```

`setup-demo-env.sh` starts a disposable Postgres container (`isola-demo-pg`) and
seeds a `myapp_dev` template database; the demos clone it per worktree. `make
clean` removes the container.

## Quick Start

```bash
# Generate all demo GIFs
make all

# Or generate individually
make tui          # TUI dashboard demo
make workflow     # Multi-worktree workflow demo
```

## Demo Files

| File | Description | Output |
|------|-------------|--------|
| `demo-workflow.tape` | Multi-worktree development workflow | `demo-workflow.gif` |
| `demo-tui.tape` | Interactive TUI dashboard demonstration | `demo-tui.gif` |

## Manual Generation

If `make` doesn't work, you can run manually:

```bash
# 1. Build isola
cd /path/to/isola
go build -o isola .

# 2. Setup demo environment
./demo/setup-demo-env.sh /tmp/isola-demo ./isola

# 3. Generate GIFs
cd /tmp/isola-demo
export PATH="/path/to/isola:$PATH"
vhs /path/to/isola/demo/demo-workflow.tape
vhs /path/to/isola/demo/demo-tui.tape
```

## Customization

Edit the `.tape` files to customize:
- `Set FontSize` - Font size (default: 14)
- `Set Width/Height` - Terminal dimensions
- `Set Theme` - Color theme (e.g., "Catppuccin Mocha", "Dracula")
- `Set PlaybackSpeed` - Speed multiplier
- `Sleep` durations - Pause between commands

See [VHS documentation](https://github.com/charmbracelet/vhs) for all options.

## Adding to README

After generating, move the GIFs and update the main README:

```markdown
## Demo

![isola workflow demo](./demo/demo-workflow.gif)
```
