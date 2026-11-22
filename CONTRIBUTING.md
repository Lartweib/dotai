# Contributing to dotai

Thanks for your interest in dotai. This guide covers how to build and test the engine, what CI checks, and how changes, decisions and docs are written.

By taking part you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report security issues privately, as [SECURITY.md](SECURITY.md) describes, never in a public issue.

## Before you start

- For anything beyond a small fix, open an issue first, so that the change can be agreed before you write it.
- Read [ARCHITECTURE.md](docs/ARCHITECTURE.md) for the shape of the engine, and the [decision records](docs/decisions/README.md) that cover the area you touch.
- Never run a change against your real harness configuration while developing it: use a temporary home directory, as the tests do.

## Requirements

- Go 1.26 or later (the version in `go.mod`).
- Node.js 22, only for the tests of the web UI's pure modules in `internal/server/webtest/`; without Node they are skipped locally and fail in CI.
- git 2.41 or later, for the tests of git packs.
- No C toolchain: the engine builds with `CGO_ENABLED=0`. `go test -race` needs cgo, so on Windows the race detector needs a C toolchain.

## Build and test

```sh
go build ./cmd/dotai          # the binary
go vet ./...
gofmt -l .                    # must print nothing
go test ./...                 # the whole suite
go test -short ./...          # skips the slowest tests, for a quick loop
go test -race ./...           # Linux and macOS, as CI runs it
```

- `go test -short` skips the heavy kill and crash matrices and the tests that build the binary. CI always runs the full suite; run it in full before you open a pull request.
- Tests never touch your real home directory: each one works in its own `t.TempDir()`. When you run the binary by hand, point `HOME` (and `USERPROFILE` on Windows) at a scratch directory first.
- `DOTAI_E2E=1 go test -count=1 -run 'WithRealRulesync$' ./internal/app/` runs the end-to-end tests with the real pinned rulesync binary: staging, the project plan, and an apply and restore of a project. They need the network, and CI runs them on every OS. `DOTAI_CACHE_DIR` names the download cache. Name a new `DOTAI_E2E` test `...WithRealRulesync` so that this command and CI pick it up.
- `DOTAI_NET_TESTS=1 go test -count=1 -run TestFetchPackFromGitHub ./internal/app/` fetches a small public repository from github.com with the guarded git options of `dotai pack add`. CI runs it in the same step as the end-to-end tests.
- Golden files live in `testdata/` next to their tests. After an intended output change, rewrite them with `go test ./internal/cli/ -update` (likewise for `internal/envelope` and `internal/plan/jsonc`), and review the diff of every golden file you commit. In `internal/packtemplate`, `-update` appends the hashes of the current template version to `hashes.txt`.

## What CI checks

The [ci workflow](.github/workflows/ci.yml) runs on Windows, Linux and macOS for every push to `main` and every pull request. A pull request is merged only when it passes:

- `gofmt -l .` prints nothing;
- `go vet ./...`;
- `staticcheck ./...` and `govulncheck ./...`, pinned in the workflow;
- `go mod verify`;
- `go test -race ./...` on Linux and macOS, and `go test ./...` on Windows, with Node 22 for the web UI tests;
- `go build ./cmd/dotai`;
- the end-to-end tests with the real rulesync binary and the github.com fetch test above, and `scripts/release.sh --self-test`;
- `shellcheck` on `install.sh` and `scripts/release.sh`. The tests of the install scripts run `install.sh` under `sh`, and `bash` when present, on Linux and macOS, and `install.ps1` under Windows PowerShell, and pwsh when present, on Windows, against a fake release on `127.0.0.1`. With `CI` set, a missing `sh` or Windows PowerShell fails them instead of skipping them; CI also runs them verbose, so its log lists every case and shell.

## Tests first

The engine is written test-first, strictly: every behavior change starts with a test that fails for the right reason, then the code that makes it pass. A bug fix comes with the test that reproduces the bug. Keep tests deterministic: no sleeps to wait for something, no real network outside the `DOTAI_E2E` and `DOTAI_NET_TESTS` tests, and no reliance on the machine's own harnesses or home directory.

## Commits and pull requests

- Commits follow [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`, with an optional scope such as `fix(plan):`. release-please builds the changelog and the next version from them, so `feat:` and `fix:` are for what a user sees ([Releasing](#releasing)). The subject is 72 characters or fewer, in the imperative mood, without a trailing period. The `commits` check (`.github/workflows/commits.yml`) holds the pull request title and every commit to this, and to the 72 characters; merge commits that GitHub writes and release-please's `chore: release <version>` pass. Run it locally with `node .github/scripts/check-commits.mjs origin/main HEAD`, and its tests with `node --test scripts/tests/check-commits.test.mjs`.
- Each commit is a reviewable unit of work that builds and passes its tests on its own: keep the tests and the docs of a change in the same commit as the code.
- A pull request does one thing, leaves something that can be run or inspected, and starts from the current `main`. Describe what changed, why, and how you verified it; the [pull request template](.github/pull_request_template.md) lists what to include.
- Update the docs a change affects in the same pull request: [CLI-JSON.md](docs/CLI-JSON.md) for any command output, [PACK-FORMAT.md](docs/PACK-FORMAT.md) for the pack contract, [HARNESS-NOTES.md](docs/HARNESS-NOTES.md) for harness behavior, and the [roadmap](docs/ROADMAP.md) when a deliverable is done.

## Releasing

The owner decides when to release by merging one pull request. Nobody computes a version or makes a tag by hand ([0052](docs/decisions/0052-release-please-owns-versions-and-changelog.md)).

[release-please](https://github.com/googleapis/release-please) (`.github/workflows/release-please.yml`) keeps one pull request open on `main`, titled `chore: release <version>`. It reads the conventional commits since the last release and proposes the next version, and it writes `CHANGELOG.md`, which is also the notes of the GitHub release. Below 1.0 a `feat:` bumps the minor, a `fix:` the patch, and a breaking change (`feat!:`, or a `BREAKING CHANGE:` footer) also the minor. `docs`, `chore`, `ci`, `build`, `test`, `refactor` and `style` commits do not bump the version and stay out of the changelog, so write a user-visible change as `feat:` or `fix:` and say in its subject what the user gets. The pull request updates itself on every push to `main`. Do not edit `CHANGELOG.md` by hand.

To release:

1. Read the release pull request (the version and the changelog) and merge it, with a merge commit like any other pull request. The required checks run on it like on any pull request.
2. The merge makes the workflow create the `v<version>` tag and a **draft** GitHub Release with the changelog as its notes, and start `.github/workflows/release.yml` on that tag. That workflow runs the tests, builds the archives and `SHA256SUMS`, signs the provenance attestation, uploads them to the draft and publishes it ([0042](docs/decisions/0042-tag-driven-release-binaries.md)). Nothing else is manual.

How it fits together, for when something breaks:

- The workflow uses the run's `GITHUB_TOKEN`, with no secret. GitHub starts no workflow for what that token creates (the release pull request, the tag) except a `workflow_dispatch`. So `release-please.yml` starts `release.yml` itself, with `gh workflow run --ref <tag>`, and runs `ci.yml` on the release pull request's branch so that the required checks exist. Because the tag push fires nothing, a release runs `release.yml` once.
- The release is a draft from the start (`draft` and `force-tag-creation` in `release-please-config.json`), so it is never public without its files. `release.yml` keeps the draft's notes, puts the toolchain line in front and publishes it. A `v*` tag pushed by a person still runs `release.yml`, which then creates the release itself: that is how to release by hand if `release-please.yml` ever fails (then change the merged release pull request's label from `autorelease: pending` to `autorelease: tagged`, or release-please opens no later release pull request), and how to try a pre-release (`v0.1.0-rc.1`) on a commit of `main`. The `release tags` ruleset does not allow deleting a tag, so a test tag stays; delete its release, not the tag.
- If `release.yml` fails after the tag exists (a failing check, say), fix `main`, then run **Actions > release > Run workflow** from the tag. It completes the same draft. A published release is never changed: it fails with an error instead.
- `bootstrap-sha` and `initial-version` in `release-please-config.json` only matter until the first release is merged: they cut the first changelog at the commit before release-please was adopted, and make the first version `0.1.0`. The summary of `v0.1.0` is in the message of the commit `docs: seed the notes of the first release, v0.1.0`. After the release, the manifest records the version and both settings can be deleted.
- `CHANGELOG.md` does not exist before the first release: release-please creates it when that pull request is merged. Once it exists, a header can be added above the first version heading and release-please keeps it.
- Moving to a GitHub App or personal access token for release-please (`token:` in the workflow) would make GitHub start the workflows by itself, and the dispatch steps could then go. Do not keep both, or each release runs twice.

### One-time setup

In Settings > Actions > General, under Workflow permissions, check **Allow GitHub Actions to create and approve pull requests**. Without it release-please cannot open the release pull request. The `release tags` ruleset must keep allowing the creation of `v*` tags (it restricts deletion, update and force-push only), or release-please cannot tag the release.

## Decision records

Significant decisions are recorded in [docs/decisions](docs/decisions/README.md), one file per decision, in a format based on MADR.

- In the docs and in code comments, "decision NNNN", or a bare number such as "0035", refers to `docs/decisions/NNNN-*.md`.
- To propose one, copy [template.md](docs/decisions/template.md) to the next free number, `NNNN-short-title.md`, with `status: proposed`, and add its row to the index. It becomes binding once it is `accepted`.
- An accepted record is never rewritten. A fact that changes without changing the decision gets a dated `Update YYYY-MM-DD:` note at the end; a changed decision gets a new record that supersedes the old one, in whole or in part.
- Numbers are sequential and never reused.

## Docs

All public docs, code comments and commit messages are in English ([0014](docs/decisions/0014-public-docs-in-english.md)), in a plain, professional tone. State verified facts with the version and OS they were checked on, and mark anything not yet built as such.

## Adding a harness

Each harness is a data adapter in [internal/harness/harness.go](internal/harness/harness.go) ([0028](docs/decisions/0028-plan-centric-hexagonal-engine.md)): the logic that plans, installs and restores is shared, and an adapter only describes the harness. An adapter holds:

- its manifest id and its rulesync target (`ID`, `Target`), and its name for people;
- where rulesync writes its user-scope files in staging (`Staging`), and the kinds of those files (`UserKinds`);
- its isolation (`Isolation`): the level, the pointers that move it to its isolated directory (an environment variable or a flag), what still leaks from the user's own setup, and whether the login carries over ([0018](docs/decisions/0018-install-modes-and-mandatory-backup.md));
- its user layout: the isolated directory under `~/.dotai/isolated/` (`Dir`, `Install`, `Moves`, [0031](docs/decisions/0031-isolated-install-root.md)), its settings file (`Settings`, [0035](docs/decisions/0035-settings-files-owned-by-key.md)), and its own configuration for the replace mode with the keep-list of what a reset never wipes (`Real`, in `replace.go`, [0038](docs/decisions/0038-replace-mode-resets-harness-to-fresh-install.md));
- its project layout (`Project`, in `project.go`): the local target of each kind, the files merged by key, and the slots it lacks ([0041](docs/decisions/0041-project-scope-local-and-untracked.md));
- the command that detection looks for and `dotai run` launches (`Command`);
- its status (`Status`, `StatusReason`): `supported`, or `preview` with the reason, which an install never selects by default ([0047](docs/decisions/0047-harness-selection-and-preview-harnesses.md)).

To add a harness:

1. Verify, on a real installation and in a temporary home, what the harness reads and writes at user and project level, how it can be isolated, and how it runs hook commands. Record each fact in [HARNESS-NOTES.md](docs/HARNESS-NOTES.md) with the version and OS it was checked on, or mark it unverified.
2. Check that the pinned rulesync renders the harness, and add its rulesync target name to `Harnesses` in [internal/pack/manifest.go](internal/pack/manifest.go), the names a manifest accepts. That list cannot derive from the adapters, since `internal/harness` imports `internal/pack`; `TestPackHarnessesAreTheAdapters` fails until the two agree.
3. Add the adapter entry to `adapters` in `internal/harness/harness.go`, with its `Real` layout in `replace.go` and its `ProjectLayout` in `project.go`. A new harness starts as a preview: `Status: Preview` with a `StatusReason` that says what is not verified yet ([0047](docs/decisions/0047-harness-selection-and-preview-harnesses.md)); `TestStatus` fails for an adapter without a status, and lists each adapter's. It is promoted to `Supported` once the pinned rulesync renders it without known problems and its HARNESS-NOTES are verified on a real install.
4. Add the harness to the table tests in `internal/harness` (`harness_test.go`, `replace_test.go`, `project_test.go` and `kind_test.go`), and to the tests of any per-harness behavior outside the adapter, such as the project settings files in `internal/app/settings.go`, the settings rules in `internal/pack/settings.go`, and the session-start hook output in `internal/app/onboarding.go`. Add its name to the harness lists in the help text of the command flags that name harnesses, such as `internal/cli/packnew.go` and `internal/cli/onboarding.go`. For the web UI, add its name (`harness.<id>`) and, while it is a preview, its reason (`reason.<id>`, without a final period) to both dictionaries of `internal/server/web/js/i18n.js`.
5. Add the harness's row to the supported-harnesses table in the [README](README.md), with its status, and a decision record when it changes the v1 targets of [0019](docs/decisions/0019-v1-harness-targets.md).
