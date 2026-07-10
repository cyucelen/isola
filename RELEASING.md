# Releasing isola

Releases are cut by [GoReleaser](https://goreleaser.com) from a pushed git tag.
Pushing a `v*` tag triggers `.github/workflows/release.yaml`, which builds the
binaries, creates the GitHub Release, publishes deb/rpm packages, and updates the
Homebrew formula in [`cyucelen/homebrew-tap`](https://github.com/cyucelen/homebrew-tap).

## Prerequisites (one time)

1. **The `isola` repo is public.** Homebrew and `go install …@latest` fetch the
   release archives and the module unauthenticated, so the repo must be public
   for them to work (a private repo fails even for someone with access, because
   the download itself carries no credentials).
2. **Set the `HOMEBREW_TAP_GITHUB_TOKEN` secret** on the `isola` repo to a
   personal access token with `contents:write` on `cyucelen/homebrew-tap`.
   Without it GoReleaser cannot push the formula.

   ```bash
   gh secret set HOMEBREW_TAP_GITHUB_TOKEN -R cyucelen/isola
   ```

## Cut a release

```bash
git tag v0.1.0          # semver; the changelog is generated from commits
git push origin v0.1.0
```

The workflow does the rest. After it finishes:

```bash
brew install cyucelen/tap/isola     # works once the repo is public + tag is out
isola version                        # should print v0.1.0 (commit, built)
```

## Verify before tagging

```bash
goreleaser check                     # validate .goreleaser.yml
goreleaser release --snapshot --clean --skip=publish   # dry run, no upload
```

The snapshot build lands binaries under `dist/`; run one to sanity-check the
version string and that `isola version` reports the tag.
