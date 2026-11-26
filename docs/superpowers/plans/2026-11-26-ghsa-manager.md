# ghsa-manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A local TUI that shows every draft argo-workflows GHSA, with its fork, fix PRs, last mirror CI run and reviews. It also runs a capped CI queue through the shared `ghsa-ci.sh`, and reports results as an advisory comment.

**Architecture:** A Go CLI (cobra) in the style of cherry-picker.
- Pure packages decide status and scheduling.
- Thin GitHub packages fetch facts using the `gh` login's token.
- A small JSON state file holds marks, the queue and in-flight runs.
- CI is started by running the shared `ghsa-ci.sh` (fetched live from `mirror-sync`) with a new `--json` mode.
- Bubble Tea renders layout A.

**Tech Stack:**
- Go 1.27.4, pinned in `flake.nix` as in todaydeck;
- `github.com/spf13/cobra`;
- `github.com/google/go-github/v90`;
- `golang.org/x/oauth2`;
- `github.com/charmbracelet/bubbletea` v1.3.10;
- `github.com/charmbracelet/lipgloss` v1.1.0;
- `github.com/stretchr/testify`;
- bash and shellcheck for the script.

**Spec:** `docs/superpowers/specs/2026-11-26-ghsa-manager-design.md`, which you must read first. Also read the shared script and its README on the mirror's `mirror-sync` branch.

## Global Constraints

- **Scope:** draft advisories only (`state=draft`) on `argoproj/argo-workflows`. Mirror: the private mirror (configured as `mirror`).
- **Token:** read from `gh auth token` only. Ignore `GITHUB_TOKEN` and `GH_TOKEN`, and have no token flag. This is the same as cherry-picker's `internal/commands/common.go`.
- **No external services:** nothing goes anywhere except `api.github.com`, `github.com` and git over SSH to the mirror. No telemetry. Don't log advisory titles or details anywhere except the screen.
- **State:** lives in `$XDG_STATE_HOME/ghsa-manager/state.json`, defaulting to `~/.local/state/ghsa-manager/state.json`. The directory is `0700`, the file `0600`. Writes are atomic (temp file then rename) and serialised by an flock on `state.json.lock`.
- **CI cap:** N in-flight `CI` workflow runs in the mirror, default **3**, set with `--max-runs`. The cap counts every queued or in-progress `ci-build.yaml` run in the mirror, whoever started it.
- **Marking:** runs once, and only for PRs without a confirmed green run on their current head. Within a GHSA, `main` goes first, and cherry-picks don't wait for it. PRs start as slots free.
- **Script:** `ghsa-ci.sh` is fetched from `mirror-sync` at run time, never vendored or pinned. Script changes must keep every existing manual invocation working.
- **Pushes:** SSH with YubiKey touches. The script runs in the background, and the TUI shows `touch YubiKey` while it runs.
- **Report comment marker:** the first line of the body is `<!-- ghsa-manager:ci-status -->`.
- **Stale:** a CI result is stale when the mirror run's head SHA differs from the fork PR's current head. A review is stale when its `commit_id` differs from the PR's current head. A base that moved after the run is a separate note, not stale.

## Review Focus

1. **A queued GHSA loses a PR or its fork, or leaves draft.** It drops out of the queue with a footer note and doesn't crash. Tests are in Task 8.
2. **`ghsa-ci.sh` exits non-zero** (a safety check refuses, a conflict, SSH fails, or nobody touches the YubiKey). The PR shows `start failed: <first stderr line>` and its slot is freed. It isn't retried automatically. Tests are in Task 8.
3. **Restart with runs in flight.** In-flight entries are reconciled with the mirror's runs, and the cap still holds. Tests are in Task 8.
4. **GitHub API errors or rate limiting during a refresh.** The last good data stays on screen, and the footer shows the error. Tests are in Task 6.
5. **A mirror run whose merged base can't be found in the log, or doesn't match the recorded base.** It's shown as `unconfirmed`, never `green`. Tests are in Task 4 for the rule and Task 5 for the parsing.

---

### Task 1: `ghsa-ci.sh` gains `--json` output and multiple PR numbers

Work in a clone of the mirror's tooling branch:

```bash
git clone --single-branch -b mirror-sync git@github.com:<mirror>.git
```

**Files:**
- Modify: `ghsa-ci.sh` (argument parsing, PR selection, a final per-PR `echo`);
- Modify: `README.md` (the modes table).

**Interfaces:**
- **Produces:** `ghsa-ci.sh [--json] <GHSA-ID> [PR-NUMBER...]`. With `--json`, stdout carries exactly one JSON object per processed PR, one per line, and all human output goes to stderr:

  ```json
  {"ghsa":"GHSA-abcd-efgh-ijkl","fork":"argoproj/argo-workflows-ghsa-abcd-efgh-ijkl","fork_pr":1,"fork_pr_url":"…","base":"main","head_sha":"<40>","base_sha":"<40>","mirror_branch":"ghsa-abcd-efgh-ijkl/main-pr1","mirror_pr_url":"…","action":"opened|updated|reopened|unchanged","conflicting":false}
  ```

  `base_sha` is the mirror base-branch SHA after any fast-forward. `reopened` is produced by Task 2; here, emit `opened` or `updated`.

- [ ] **Step 1:** Extend argument parsing.
  - Accept `--json`.
  - Accept any number of PR numbers after the GHSA ID; the existing code takes at most one.
  - Build the selection filter as `select(.number == (N1, N2, …))`.
  - Keep `--all`, `--cleanup`, `--dry-run` and the URL form unchanged.
  - Reject PR numbers combined with `--all` or `--cleanup`, as today.
- [ ] **Step 2:** When `--json` is set, make every human `echo` go to stderr, including the current final `url -> mirror_pr` line. Then print the JSON object with `jq -cn --arg …`. Add `jq` to the requirements check.
- [ ] **Step 3:** Run `shellcheck -S warning ghsa-ci.sh`. Expected: no output.
- [ ] **Step 4:** Verify manual use is unchanged. Run `ghsa-ci.sh --dry-run GHSA-abcd-efgh-ijkl`; expected: the same stderr lines as before and the `url -> (dry-run)` line.
- [ ] **Step 5:** Run `ghsa-ci.sh --dry-run --json GHSA-abcd-efgh-ijkl 1 2`. Expected: two lines on stdout that each parse with `jq -e .ghsa`, and nothing else on stdout.
- [ ] **Step 6:** Commit, signed and signed off, then push to `mirror-sync`. The push needs a YubiKey touch.

### Task 2: `ghsa-ci.sh` reopens after the base moves, and reuses SSH connections

**Files:**
- Modify: `ghsa-ci.sh` (the per-PR loop after the base fast-forward; the SSH push setup);
- Modify: `README.md` ("What the script does").

**Interfaces:**
- **Consumes:** the Task 1 JSON line.
- **Produces:** `action:"reopened"` and `action:"unchanged"`.

- [ ] **Step 1:** Detect "only the base moved": the mirror branch already exists, its tip equals the fork head, and an open mirror PR exists. In that case:
  - poll `gh api repos/$MIRROR/git/ref/pull/<n>/merge`, then that commit's `parents[0].sha`, until it equals the new `base_sha`;
  - between polls, call `gh pr view --json mergeable`, which nudges GitHub into recomputing the merge;
  - poll every 5s, giving up after 180s;
  - then `gh pr close` followed by `gh pr reopen`, and emit `reopened`;
  - on timeout, log a warning and emit `unchanged`.

  If neither the head nor the base moved, emit `unchanged` and don't reopen. Replace the misleading "CI restarts there" message with an accurate one for each action.
- [ ] **Step 2:** SSH multiplexing for every push to the mirror. Append `-o ControlMaster=auto -o ControlPath=$WORK/ssh-%C -o ControlPersist=120` to the push SSH command, keeping any `core.sshCommand` the user already has. In the EXIT trap, run `ssh -O exit -o ControlPath=$WORK/ssh-%C git@github.com` before `rm -rf "$WORK"`.
- [ ] **Step 3:** Run `shellcheck -S warning ghsa-ci.sh`. Expected: no output.
- [ ] **Step 4:** Live check, which needs YubiKey touches. Run `ghsa-ci.sh --json GHSA-abcd-efgh-ijkl 1` twice in a row. Expected:
  - the second run emits `"action":"unchanged"`;
  - each run takes one touch for all of its SSH operations;
  - no new CI run starts in the mirror for the second invocation.
- [ ] **Step 5:** Commit and push to `mirror-sync`.

### Task 3: Scaffold the module, dev tooling, token, GitHub client and root command

The dev tooling replicates `~/sources/todaydeck`'s setup, which was adapted from steadystate-mono. Copy those files, keeping their header comments but saying "Adapted from todaydeck's". Leave out everything this local, undeployed repo has no use for: Docker and hadolint, sqlc and `gen-check`, `.ci/` and manifests, renovate (there's no remote for it to run against), crane and kubectl.

**Files:**
- Create:
  - `go.mod` (`module github.com/alan/ghsa-manager`, `go 1.27.4`) and `main.go`;
  - `flake.nix` and `flake.lock`. Copy todaydeck's flake, including the pinned `goVersion` and the src-hash override. Keep these packages: `go`, `gnumake`, `golangci-lint`, `govulncheck`, `gitleaks`, `shellcheck`, `jq`, `lefthook`, `nodejs_22`. Set `GOTOOLCHAIN = "local"`.
  - `.envrc` (`use flake` plus `source_env_if_exists .envrc.local`) and `.editorconfig`, copied verbatim;
  - `.golangci.yaml`, copied verbatim, then drop any exclusions that name todaydeck paths;
  - `lefthook.yml`, keeping:
    - pre-commit: `go-lint` (`make lint`), `go-pin-check` and `gitleaks`;
    - pre-push: `go-test`;
    - commit-msg: `commitlint`;
  - `commitlint.config.js`, copied verbatim;
  - `Makefile`, with the targets `all` (lint, test, build), `build` (`go build -o bin/ghsa-manager .`), `test` (`go test -race ./...`), `lint`, `fmt`, `tidy`, `gitleaks`, `vuln`, `go-pin-check`, `go-toolchain-sync`, and `check` (go-pin-check, lint, test, gitleaks, vuln). The recipes are todaydeck's.
  - `scripts/go-toolchain-sync.sh`, copied from todaydeck;
  - `.gitignore`: add `.envrc.local`, `.direnv/` and `bin/`;
  - `internal/gh/token.go` and `internal/gh/client.go`;
  - `CLAUDE.md`, a short note on build and test (`nix develop` or direnv, then `make check`) and the security rules from Global Constraints.
- Test: `internal/gh/token_test.go`

**Interfaces:**
- **Produces:**
  - `gh.Token(ctx context.Context) (string, error)`, which runs `gh auth token`, trimmed. The `gh` path is held in a package variable `ghBin` so tests can override it.
  - `gh.NewClient(ctx context.Context, token string) *github.Client`, a go-github v90 client;
  - the constants `gh.Upstream = "argoproj/argo-workflows"`, `gh.Mirror = "<mirror>"`, `gh.Owner = "argoproj"`.

- [ ] **Step 1: Write the failing tests.** `TestToken_TrimsOutput` uses a fake `gh` script that prints `abc\n` and expects `"abc"`. `TestToken_ErrorIncludesStderr` uses a fake that exits 1 with "no oauth token found", and expects the error to contain that text. `TestToken_IgnoresEnv` sets `GH_TOKEN=x`, and expects the fake still to be called and its output used.
- [ ] **Step 2:** Run `go test ./internal/gh/...`. Expected: FAIL, undefined `Token`.
- [ ] **Step 3:** Implement `Token` and `NewClient`. Make root `ghsa-manager` a cobra command whose `RunE` is wired in Task 10; until then it prints "TUI not built yet".
- [ ] **Step 4:** Inside the dev shell, run `lefthook install`, then `make check`. Expected: PASS. That means `go pin coherent: toolchain and go.mod both go1.27.4`, lint clean, tests pass, no gitleaks findings, and govulncheck clean.
- [ ] **Step 5:** Commit. The lefthook hooks run on this first commit, including commitlint on the message.

### Task 4: Domain model and status rules (pure)

**Files:**
- Create: `internal/model/model.go`, `internal/model/status.go`
- Test: `internal/model/status_test.go`

**Interfaces:**
- **Produces:**
  ```go
  type Review struct{ User, State, CommitID string } // State: "APPROVED" | "CHANGES_REQUESTED" (others ignored)
  type CIState int // CINever, CIRunning, CIGreen, CIRed, CICancelled, CIUnconfirmed
  type CI struct {
      State CIState; RunURL string; FinishedAt time.Time
      RunHeadSHA, MergedBaseSHA, CurrentBaseSHA string // MergedBaseSHA from the run log; "" if unknown
      SkippedTestJobs []string
  }
  type FixPR struct{ Number int; Base, HeadSHA, URL, Title string; Reviews []Review; CI CI; StartError string }
  type Advisory struct{ ID, Summary, URL, Fork string; PRs []FixPR }
  type Status int // NoFork, ForkNoPRs, NeedsCI, NeedsReview, Ready
  func (c CI) Stale(head string) bool               // RunHeadSHA != "" && RunHeadSHA != head
  func (c CI) BaseMoved() bool                      // MergedBaseSHA != "" && CurrentBaseSHA != "" && neither is a prefix of the other
  func (p FixPR) GreenOnHead() bool                 // CI.State == CIGreen && !CI.Stale(HeadSHA)
  func (p FixPR) LatestReviews() map[string]Review  // latest review per user, in API order
  func (p FixPR) ApprovedOnHead() bool              // some latest review is APPROVED with CommitID == HeadSHA
  func (p FixPR) BlockedOnHead() bool               // some latest review is CHANGES_REQUESTED with CommitID == HeadSHA
  func (a Advisory) Status() Status
  func (a Advisory) MainNotGreen() bool             // a main PR exists and !GreenOnHead()
  func ClassifyRun(conclusion string, recordedBase, mergedBase string) CIState
  ```
- **ClassifyRun:**
  - `success` with `mergedBase` empty gives `CIUnconfirmed`;
  - `success` with `recordedBase` non-empty and not prefix-equal to `mergedBase` gives `CIUnconfirmed`;
  - otherwise `success` gives `CIGreen`;
  - `failure` and `timed_out` give `CIRed`;
  - `cancelled` gives `CICancelled`;
  - `""` (still running) gives `CIRunning`.
- **Status:**
  - no fork: `NoFork`;
  - fork with zero PRs: `ForkNoPRs`;
  - any PR not `GreenOnHead`: `NeedsCI`;
  - any PR not `ApprovedOnHead`, or any `BlockedOnHead`: `NeedsReview`;
  - otherwise: `Ready`.

- [ ] **Step 1: Write a table-driven failing test per rule.** The rows must include:
  - an approved PR with no CI is `NeedsCI`;
  - a green PR whose approval's `CommitID` is an older SHA is `NeedsReview`;
  - an approval followed by a later CHANGES_REQUESTED from the same user, both on the head, is `NeedsReview`;
  - a CHANGES_REQUESTED on an old SHA followed by no new review, with another user's approval on the head, is `Ready`;
  - `ClassifyRun("success","abc1234","abc1234def…")` is `CIGreen`;
  - `ClassifyRun("success","abc","")` is `CIUnconfirmed`;
  - `ClassifyRun("success","","fff")` is `CIGreen`, for a manual run with no recorded base;
  - `ClassifyRun("success","aaa","bbb")` is `CIUnconfirmed`;
  - `MainNotGreen` is true when the `main` PR is red and false when there's no `main` PR.
- [ ] **Step 2:** Run `go test ./internal/model/...`. Expected: FAIL.
- [ ] **Step 3:** Implement the types and functions above.
- [ ] **Step 4:** Run `go test ./internal/model/...`. Expected: PASS.
- [ ] **Step 5:** Commit.

### Task 5: Mirror CI facts

**Files:**
- Create: `internal/mirror/mirror.go`, `internal/mirror/mergelog.go`
- Test: `internal/mirror/mergelog_test.go`, `internal/mirror/mirror_test.go` (using an `httptest` server and go-github's `WithEnterpriseURLs`, or `BaseURL`)

**Interfaces:**
- **Consumes:** `gh.Mirror` and `model.CI` (Tasks 3 and 4).
- **Produces:**
  ```go
  func BranchName(ghsa, base string, pr int) string                 // "ghsa-<lowercase id>/<base>-pr<N>"
  func ParseMergedBase(log string) (head, base string, ok bool)     // matches `Merge ([0-9a-f]{40}) into ([0-9a-f]{7,40})`
  type Facts struct{ c *github.Client; logCache map[int64]string }
  func NewFacts(c *github.Client) *Facts
  func (f *Facts) LatestCI(ctx context.Context, branch string, recordedBase, currentBase string) (model.CI, error)
  func (f *Facts) InFlight(ctx context.Context) (int, error)        // count of queued + in_progress ci-build.yaml runs
  ```
- **`LatestCI`:**
  - gets the newest `ci-build.yaml` run with `event=pull_request` on `branch`, using `ListWorkflowRunsByFileName`; none gives `CINever`;
  - lists its jobs and sets `SkippedTestJobs` to skipped jobs whose names match `^((Windows )?Unit Tests|E2E Tests( \(|$)|E2E UI)`, the regex `ghsa-ci-report.yaml` uses;
  - for a completed run, fetches the log of the `Get changed files` job, follows the redirect, caches it by job ID, and runs `ParseMergedBase`;
  - sets `State` using `model.ClassifyRun`.

- [ ] **Step 1: Write the failing tests.**
  - `ParseMergedBase` on a real-shaped line `HEAD is now at f3478d3 Merge b2b514b48d6c954e11e21cacc2356b4710d5414e into a0ff8ba5d56d79772387b10a88416449c056dd65` returns that head and base. On a log with no such line, it returns `ok=false`.
  - `BranchName("GHSA-ABCD-efgh-ijkl","release-4.1",2)` returns `ghsa-abcd-efgh-ijkl/release-4.1-pr2`.
  - `LatestCI` against `httptest` fixtures: a successful run whose log base matches gives `CIGreen` with skipped jobs listed; a log without a merge line gives `CIUnconfirmed`; no runs gives `CINever`.
- [ ] **Step 2:** Run `go test ./internal/mirror/...`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/mirror/...`. Expected: PASS.
- [ ] **Step 5:** Commit.

### Task 6: Advisory source and the `status` command

**Files:**
- Create: `internal/source/source.go`, `cmd/status.go`, `internal/render/plain.go`
- Test: `internal/source/source_test.go` (httptest), `internal/render/plain_test.go`

**Interfaces:**
- **Consumes:** `mirror.Facts` and `mirror.BranchName` (Task 5), and the model (Task 4).
- **Produces:**
  ```go
  type Snapshot struct{ Advisories []model.Advisory; FetchedAt time.Time; Err error }
  type Source struct{ c *github.Client; facts *mirror.Facts }
  func (s *Source) Fetch(ctx context.Context, recordedBases map[string]string) Snapshot // key: mirror branch name
  func Plain(w io.Writer, snap Snapshot, queue []string) // layout A as plain text, no colour
  ```
- **`Fetch`:**
  - lists advisories with `SecurityAdvisories.ListRepositorySecurityAdvisories(…, State: "draft")`, paginated, and takes the fork from `PrivateFork`;
  - for each fork, lists its open PRs and each PR's reviews;
  - gets the current mirror base SHA per base branch once per fetch;
  - calls `LatestCI` for each PR;
  - on any error, returns `Snapshot{Err: err}` with no partial advisories. The caller keeps its previous snapshot (Review Focus 4).
- **The `status` command:** `ghsa-manager status` fetches once, then prints `Plain`, ordered queue first, then by GHSA ID.

- [ ] **Step 1: Write the failing tests.**
  - The httptest fixtures cover two drafts: one with no fork, and one with a fork and two PRs with reviews.
  - Expect correct `Advisory` assembly, and expect `Status()` to give `NoFork` and `NeedsCI`.
  - A 500 response on the PR list gives `Snapshot.Err != nil` and `len(Advisories) == 0`.
  - The `Plain` golden test matches a fixed snapshot to an expected string containing `GHSA-aaaa-bbbb-cccc  needs review`, `main         #1   ✓ green` and `approved (stale)`.
- [ ] **Step 2:** Run `go test ./internal/source/... ./internal/render/...`. Expected: FAIL.
- [ ] **Step 3:** Implement `Fetch`, `Plain` and `cmd/status.go`.
- [ ] **Step 4:** Run the tests. Expected: PASS. Then run `go run . status` against real GitHub; expected: every draft listed.
- [ ] **Step 5:** Commit.

### Task 7: Local state file

**Files:**
- Create: `internal/state/state.go`
- Test: `internal/state/state_test.go`

**Interfaces:**
- **Produces:**
  ```go
  type Flight struct{ GHSA string; PR int; Branch, HeadSHA, BaseSHA string; StartedAt time.Time }
  type State struct {
      Queue      []QueueEntry      `json:"queue"`       // ordered
      InFlight   []Flight          `json:"in_flight"`
      CommentIDs map[string]int64  `json:"comment_ids"` // GHSA -> advisory comment ID
      Bases      map[string]string `json:"bases"`       // mirror branch -> base SHA recorded at trigger
  }
  type QueueEntry struct{ GHSA string; Pending []int; Failed map[int]string }
  func Path() string                               // honours XDG_STATE_HOME
  func Load(path string) (*State, error)           // missing file gives an empty State
  func Update(path string, fn func(*State) error) error // flock on path+".lock", load, fn, atomic save at 0600
  ```

- [ ] **Step 1: Write the failing tests.**
  - `Load` of a missing file returns an empty state.
  - After `Update` saves, `os.Stat` shows mode `0600`, and the directory shows `0700`.
  - Two goroutines each run `Update` 50 times, appending to `Queue`; the final length is 100.
  - Corrupt JSON returns an error and doesn't overwrite the file.
- [ ] **Step 2:** Run `go test ./internal/state/...`. Expected: FAIL.
- [ ] **Step 3:** Implement, using `golang.org/x/sys/unix.Flock` the way cherry-picker's `internal/lockfile` does.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5:** Commit.

### Task 8: Scheduler and script runner

**Files:**
- Create: `internal/queue/queue.go`, `internal/runner/runner.go`
- Test: `internal/queue/queue_test.go`, `internal/runner/runner_test.go`

**Interfaces:**
- **Consumes:** `state.State` (Task 7), `model.Advisory` (Task 4), `mirror.Facts.InFlight` (Task 5).
- **Produces:**
  ```go
  // queue
  func Mark(s *state.State, a model.Advisory) bool // enqueue the non-GreenOnHead PRs, main first; false if none
  func Unmark(s *state.State, ghsa string)
  func Move(s *state.State, ghsa string, delta int)
  type Start struct{ GHSA string; PR int }
  func Next(s *state.State, snap []model.Advisory, mirrorInFlight, max int) []Start // at most max-mirrorInFlight starts, in queue order
  func Reconcile(s *state.State, snap []model.Advisory) []string // drop entries whose GHSA/PR/fork vanished or left draft; returns footer notes
  // runner
  type Result struct{ GHSA, Fork, Base, HeadSHA, BaseSHA, MirrorBranch, MirrorPRURL, Action string; ForkPR int; Conflicting bool }
  type Runner struct{ ScriptURL string; Bash string }  // ScriptURL: repos/<mirror>/contents/ghsa-ci.sh?ref=mirror-sync
  func (r Runner) Run(ctx context.Context, ghsa string, prs []int) ([]Result, error) // fetch the script live to a 0700 temp dir, run `bash script --json GHSA prs…`, parse stdout lines
  ```
- **When a run finishes** (`Run` returns): each started PR moves from `Pending` to `InFlight`, with `BaseSHA` recorded in `state.Bases[branch]`. On error, `Failed[pr]` is set to the first stderr line and the PR leaves `Pending`.
- **Retiring flights:** an `InFlight` entry is retired when the mirror shows a completed run for its branch and head, created after `StartedAt`.

- [ ] **Step 1: Write the failing tests.**
  - `Mark` enqueues only the non-green PRs, `main` first.
  - `Next` with max 3 and 1 in the mirror returns 2 starts, crossing GHSA boundaries in queue order.
  - `Next` with 3 in flight returns none.
  - Review Focus 1: `Reconcile` drops a GHSA that has left the snapshot, and a PR that has closed, and returns notes.
  - Review Focus 3: after a restart, a state with 2 in flight plus a mirror count of 2 gives `Next` with max 3 returning 1 start.
  - Review Focus 2: `Runner.Run` with a fake script that exits 1 with "error: … SonarCloud" returns an error whose message is that first line. With a fake that prints two JSON lines, it returns 2 parsed `Result`s.
- [ ] **Step 2:** Run `go test ./internal/queue/... ./internal/runner/...`. Expected: FAIL.
- [ ] **Step 3:** Implement. `Runner` fetches the script with the GitHub contents API using `Accept: application/vnd.github.raw`.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5:** Commit.

### Task 9: Advisory comment reporter

**Files:**
- Create: `internal/report/report.go`
- Test: `internal/report/report_test.go` (httptest)

**Interfaces:**
- **Consumes:** the model (Task 4) and state (Task 7).
- **Produces:**
  ```go
  const Marker = "<!-- ghsa-manager:ci-status -->"
  func Body(a model.Advisory, now time.Time) string // Marker line, then a table: | Fix PR | Branch | CI | with a run link, then "_Last updated <UTC>_"
  func Upsert(ctx context.Context, c *github.Client, s *state.State, a model.Advisory) error
  ```
- **`Upsert`:**
  - uses `state.CommentIDs[ghsa]` if set;
  - otherwise finds the first comment whose body starts with `Marker`, or with `**CI status for the fix PRs**` (to adopt a hand-posted test comment);
  - otherwise posts a new comment;
  - sends raw requests with `c.NewRequest`, because go-github has no advisory-comments methods: `GET/POST repos/argoproj/argo-workflows/security-advisories/{ghsa}/comments` and `PATCH …/comments/{id}`;
  - records the ID in state.
- **When to report:** once a GHSA has no `Pending` and no `InFlight` entries left, it's called once, then the GHSA leaves the queue.

- [ ] **Step 1: Write the failing tests.**
  - The `Body` golden test covers green, red with skipped jobs listed, `unconfirmed`, and start-failed rows.
  - `Upsert` with no comments POSTs. With an existing marker comment it PATCHes that ID. With a legacy-titled comment it PATCHes it and records the ID.
- [ ] **Step 2:** Run `go test ./internal/report/...`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5:** Commit.

### Task 10: The TUI (layout A)

**Files:**
- Create: `internal/tui/model.go`, `internal/tui/view.go`, `internal/tui/keys.go`
- Modify: `main.go` (root `RunE` starts the TUI; adds `--max-runs` with default 3 and `--refresh` with default 2m)
- Test: `internal/tui/view_test.go`, `internal/tui/model_test.go`

**Interfaces:**
- **Consumes:** everything above.
- **Produces:** the `ghsa-manager` binary's default behaviour.

**Behaviour:**
- **Rows:** grouped by GHSA as in the mockup.
  - Each GHSA row shows: queue position or `·`, GHSA ID, status word, and `fork ✓` or `no fork`.
  - It's flagged `⚠ main not green` when `MainNotGreen()` and cherry-picks are queued or in flight.
  - PR rows show: branch, `#N`, CI (`✓ green` / `✗ red` / `● running` / `○ cancelled` / `? unconfirmed` / `never`, with an age, `(stale)` and `(base moved)` notes, and skipped jobs), and reviews (`✓ user` / `✗ user`, with `(stale)` for an off-head review).
- **Footer:** `running X/N · queued Y`, plus the last error and Reconcile notes.
- **Keys:**
  - `c`: mark;
  - `u`: unmark;
  - `J`/`K`: move;
  - `enter`/`o`: open the advisory URL;
  - `p`: open the selected PR;
  - `f`: make a fork, after a `y/n` confirm, using `CreateTemporaryPrivateFork`;
  - `r`: refresh;
  - `q`: quit.
  - URLs open with `xdg-open`.
- **Loop:**
  - a `tea.Tick` every `--refresh` runs `Source.Fetch`, then `queue.Reconcile`, retiring flights;
  - it reports for finished GHSAs (Task 9), then calls `queue.Next`;
  - each `Start` group per GHSA runs `Runner.Run` in a `tea.Cmd`;
  - while a run is in progress, the footer shows `pushing GHSA-… (touch YubiKey)`;
  - only one `Runner.Run` executes at a time, so touches are never ambiguous.

- [ ] **Step 1: Write the failing tests.**
  - The `view_test` golden output for a fixed snapshot and state, with lipgloss forced to no colour, contains the mockup's rows, including `⚠ main not green` and `approved (stale)`.
  - The `model_test` sends `c` on a selected GHSA and expects the state queue to gain it with non-green PRs only. It sends `J`, and expects the order to change.
  - A refresh error keeps the previous snapshot rendered, with the error in the footer.
  - While a fake runner is running, the footer contains `touch YubiKey`.
- [ ] **Step 2:** Run `go test ./internal/tui/...`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `make check`. Expected: PASS.
- [ ] **Step 5:** Smoke test by running `go run .` against real GitHub. Expected: drafts listed. Mark an advisory whose PRs are all green on their heads, so expect the footer note "nothing to run".
- [ ] **Step 6:** Commit.
