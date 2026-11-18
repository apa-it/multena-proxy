# Releasing

Versioning follows [Semantic Versioning](https://semver.org/) derived from
[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
**Tagging and releasing stay a manual, human-approved step.** Nothing in
this repository auto-tags, auto-releases, or auto-deploys to Pages.

## Commit message convention

Use Conventional Commits for every commit that should affect the
calculated version:

- `feat: ...` → minor bump (`1.1.0`)
- `fix: ...` → patch bump (`1.0.1`)
- `feat!: ...` or a `BREAKING CHANGE:` footer → major bump (`2.0.0`)
- anything else (`chore:`, `docs:`, `test:`, `refactor:`, merge commits,
  etc.) → no version impact on its own

The CI calculation (see below) walks the plain commit log via the GitHub
API, including merge commits. A merge commit's own message (e.g. "Merge
pull request #22 from ...") does not match the Conventional Commits
pattern, so it is simply ignored by the analyzer — it neither triggers
nor suppresses a bump. What matters is whether the merged branch itself
contains `feat:`/`fix:`/`BREAKING CHANGE:` commits. This repo merges PRs
with regular merge commits (no squash), so those commits are visible to
the analyzer as-is; no first-parent-only or PR-title-based analysis is
needed to avoid false bumps from merge noise.

## How the next version is calculated

The `Next Version (dry-run)` workflow
(`.github/workflows/next-version.yml`) runs
[go-semantic-release](https://github.com/go-semantic-release/semantic-release)
in `--dry` mode on every PR against `main` and on every push to `main`.
It:

- never tags, never creates a GitHub release, never publishes an image
  or Helm chart, never touches `release.yml` or `chart-pages.yml`;
- only has `contents: read` and `pull-requests: write` (to post a PR
  comment) — it has no permission to create tags or releases;
- writes the computed next version (or "no release required") to the
  job summary and, on pull requests, as a sticky PR comment.

### Initial version

No tag exists in this repository yet. go-semantic-release's default
behaviour (without `--allow-initial-development-versions`) treats a
missing/`0.0.0` baseline as major version `0` and always forces a major
bump on the very first calculation — regardless of which commit types
are present. This means the dry-run correctly reports **v1.0.0** as the
first version, which matches the desired starting point. No custom
"initial version" option is required; this was verified by running the
dry-run locally against this repository's actual history (first-run
output: `new version: 1.0.0`).

## Actually releasing

1. Merge the PR(s) you want included in the release into `main`.
2. Check the `Next Version (dry-run)` job summary / PR comment for the
   computed next version.
3. A human reviews and confirms the version, then runs manually:

   ```bash
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```

4. Pushing that tag triggers the existing, unchanged `release.yml`
   (image publish) and, for chart releases, the existing
   `multena-proxy-X.Y.Z` tag convention / `chart-pages.yml` flow. See
   those workflows for details — this document does not change their
   trigger logic.

## Edge image (`main` tag)

Separately from the above, `.github/workflows/edge.yml` publishes
`ghcr.io/apa-it/multena-proxy:main` (plus the commit SHA) on every push
to `main`, through the same Trivy severity gate. It is a rolling
dev/edge build with no version guarantee — never a substitute for the
manual `vX.Y.Z` release above, and not for production use. See the
[Image Tags](README.MD#image-tags) section in the README for details.
