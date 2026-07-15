---
name: isola-init
description: Configure isola for a repository — discover its dev processes, write .isola.toml (services, per-service env with ${...} references, per-worktree databases), and verify with `isola up` / `isola ls`. Use when setting up isola in a new repo, when asked to initialize or configure isola, or right after installing the isola CLI.
---

# isola-init

Set up isola for **this** repository, then verify it. Follow
**references/setup.md** for the full walkthrough; work from what the repo
actually uses, don't assume a language, framework, or OS:

1. Install the `isola` CLI if it isn't on `PATH` (`command -v isola`).
2. Discover the repo's long-running dev processes.
3. `isola init`, then write `.isola.toml`: services, per-service env, and any
   per-worktree databases.
4. Verify with `isola up` and `isola ls`.
5. `isola hooks install` so new worktrees start themselves (`--shared` shares it
   with the team; `isola hooks status` shows state). For Orca users, `isola orca`
   wires `isola up` into `orca.yaml` instead.

## References

Read the file that matches the step (don't load them all up front):

- **references/install.md** — installing the `isola` CLI (step 1).
- **references/setup.md** — the full setup walkthrough (steps 2-5): discovering
  dev processes; writing services, per-service env `${...}` references (including
  `.url` vs `.direct_url`), and accessories; verifying; and the worktree hook.

This skill only covers first-time setup. The separate **isola** skill covers
running isola afterwards.
