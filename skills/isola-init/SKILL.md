---
name: isola-init
description: Configure isola for a repository — discover its dev processes, write .isola.toml (services, per-service env with ${...} references, per-worktree databases), and verify with `isola up` / `isola ls`. Use when setting up isola in a new repo, when asked to initialize or configure isola, or right after installing the isola CLI.
---

# isola-init

Set up isola for **this** repository, then verify it. Follow
**references/setup.md** for the full walkthrough; work from what the repo
actually uses, don't assume a language, framework, or OS:

1. Install the `isola` CLI if it isn't on `PATH` (`command -v isola`), following
   [references/install.md](references/install.md).
2. Discover the long-running dev processes (from `package.json`, `Makefile`,
   `Procfile`, `docker-compose.yml`, etc.).
3. `isola init`, then write `.isola.toml`: a `[services.<name>]` per process
   (each command must bind `$PORT`), per-service `env` (referencing
   `${accessories.<name>.url}` / `${services.<name>.url}` as needed), and any
   per-worktree database under `[accessories.<name>]`.
4. Verify with `isola up` then `isola ls`, and open a URL to confirm.

To operate isola day to day afterwards (up/down/ls/dash/proxy/logs, databases,
troubleshooting), use the separate **isola** skill.
