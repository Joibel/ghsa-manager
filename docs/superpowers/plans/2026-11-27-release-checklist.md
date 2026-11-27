# Release Checklist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every draft advisory gets a six-item release checklist (branches,
description, CVSS, CVE, affected versions, credits). The main screen shows an
`n/6` count and a checklist screen sits behind `l`. `a` opens an interactive
Claude Code session for the advisory in a tmux window.

**Architecture:** Pure rules go in `internal/model/checklist.go` and a new
`internal/versions` range parser. Ticks, waivers and CVE-request records live
in the existing state file, keyed by advisory. `internal/source` also reads
the advisory's own fields and the upstream's release branches and tags.
`internal/session` launches tmux and claude through an injected runner,
copying todaydeck's `internal/workspace`. The TUI gains a second screen mode.

**Tech Stack:** Go, go-github v90, cobra, Bubble Tea/lipgloss, testify,
`github.com/google/uuid` (new; for UUIDv5 session IDs), tmux, Claude Code CLI.

**Spec:** `docs/superpowers/specs/2026-11-27-release-checklist-design.md` (it
builds on `docs/superpowers/specs/2026-11-26-ghsa-manager-design.md`). Read
both.

## Global Constraints

- Only talk to `api.github.com`, `github.com` and the mirror over SSH. The one
  exception is the interactive Claude session this tool launches. *(CLAUDE.md,
  amended in Task 8)*
- The token comes from `gh auth token` only, never `GITHUB_TOKEN`/`GH_TOKEN`.
- Never log advisory titles or details. They go on the screen only. The state
  file stores hashes of ticked values, never their text.
- State stays in `$XDG_STATE_HOME/ghsa-manager/` (dir `0700`, files `0600`),
  through the existing `state.Update` (flock + atomic write).
- The tmux window name and `@ghsa_key` are the GHSA ID only, never the title.
- The claude command line carries only the GHSA ID, the upstream, links, the
  fork name, the names of open items and fixed rule text. No advisory text.
- Claude is launched with `--strict-mcp-config --mcp-config '{"mcpServers":{}}'`,
  both when starting a session and when resuming one. `gh api` isn't blocked.
- The checklist's item order and keys, used everywhere: `branches`,
  `description`, `cvss`, `cve`, `versions`, `credits`.
- Expected branches: `main` + the newest `supported_branches` (default 2)
  `release-X.Y` branches that have a `vX.Y.0` tag and whose minor a
  vulnerable range covers + every `release-X.Y` branch without a `vX.Y.0`
  tag.
- `make check` passes after each task. Commits are conventional commits
  (commitlint runs).

## Review Focus

1. **Real-world range strings:** `>= 4.0.0, < 4.0.7`, `<4.0.7`, `<= 3.6.10`,
   `= 4.1.0`, `>= 4.1.0-rc1, < 4.1.3`, a `v` prefix, extra spaces, and
   several `vulnerabilities[]` entries (for v3 and v4 module paths) should all
   parse and combine as one union. A range that fails to parse makes the
   branches item say "can't read affected versions". It must not silently
   expect no branches. *(Task 1 and Task 3 tests)*
2. **Reordered API arrays mustn't unset ticks:** GitHub may return
   `vulnerabilities` or `credits_detailed` in a different order. The hash
   sorts before hashing, so the same values give the same hash. *(Task 3
   test)*
3. **An old state file** with no `checks` key loads as empty checks. Ticking
   still works and saves at `0600`. *(Task 3 test)*
4. **Launching outside tmux, with no checkout, or with no `claude` on PATH**
   puts a clear note in the footer and opens no window. A missing checkout
   is never created. *(Task 8 tests)*
5. **`--strict-mcp-config` really drops every MCP server,** including
   claude.ai connectors and plugin servers. This can't be unit-tested, so
   Task 8 ends with a manual check (`/mcp` in a launched session lists
   none). If it isn't empty, stop and report back. Don't ship.

---

### Task 1: Version range parser

**Files:**
- Create: `internal/versions/versions.go`
- Test: `internal/versions/versions_test.go`

**Interfaces:**
- Produces:
  - `type Version struct{ Major, Minor, Patch int; Pre string }`
  - `func Parse(s string) (Version, error)`: an optional `v` prefix, then
    `X.Y.Z` with an optional `-pre` suffix.
  - `func (v Version) Less(o Version) bool`, using semver precedence: a
    pre-release sorts before its release, and pre strings compare
    lexically.
  - `type Range struct` (unexported fields)
  - `func ParseRange(s string) (Range, error)`: comma-separated constraints,
    each `>=`, `>`, `<=`, `<` or `=` followed by a version, with spaces
    allowed anywhere. A constraint with no operator means `=`. An empty
    string is an error.
  - `func (r Range) CoversMinor(major, minor int) bool`: true when some
    version from `major.minor.0-<any pre>` up to, but not including,
    `major.(minor+1).0-<lowest pre>` satisfies every constraint.
  - `func AnyCoversMinor(ranges []string, major, minor int) (bool, error)`:
    the union of the ranges. It returns the first parse error it hits.

- [ ] **Step 1: Write failing tests.** `TestParseRange` is table-driven over
  the Review Focus #1 strings, plus errors for `""`, `>= `, `~> 4.0`. Then
  `TestCoversMinor`:
  - `>= 4.0.0, < 4.0.7`: covers 4.0, not 4.1 or 3.7.
  - `< 3.7.0`: covers 3.6, not 3.7.
  - `<= 3.6.10`: covers 3.6.
  - `= 4.1.0`: covers 4.1 only.
  - `>= 4.1.0-rc1, < 4.1.3`: covers 4.1.
  - `< 4.2.0`: doesn't cover 4.2 (4.2.0 itself is outside), but
    `< 4.2.0-rc2` does cover 4.2, because 4.2.0-rc1 is inside.

  Finally, `TestAnyCoversMinor`: `{">= 4.0.0, < 4.0.7", "< 3.7.5"}` covers
  4.0 and 3.7 but not 4.1, and `{"bad"}` returns an error.
- [ ] **Step 2:** `go test ./internal/versions/` fails because the package
  doesn't exist.
- [ ] **Step 3: Implement.** For `CoversMinor`, treat each range as an
  interval with open or closed ends, intersect its constraints into one
  interval, then check whether it overlaps the minor's half-open interval.
  Compare versions with `Less`. Write the minor's bounds as
  `Version{Major: X, Minor: Y, Pre: "0"}` (the lowest pre) up to
  `Version{Major: X, Minor: Y + 1, Pre: "0"}` exclusive.
- [ ] **Step 4:** `go test ./internal/versions/` passes.
- [ ] **Step 5:** commit `feat(versions): parse advisory version ranges`.

### Task 2: Read the advisory's fields and the release branches

**Files:**
- Modify: `internal/model/model.go` (new fields and types)
- Modify: `internal/source/source.go` (fill them; add `Snapshot.Releases`)
- Test: `internal/source/source_test.go`

**Interfaces:**
- Produces, in `model`:
  - `type Vulnerability struct{ Package, Range, Patched string }`. `Patched`
    is `patched_versions`, or `first_patched_version.identifier` when that's
    empty.
  - `type Credit struct{ User, Type, State string }`
  - `type ReleaseBranch struct{ Name string; Major, Minor int; GA bool }`.
    `GA` means a `vX.Y.0` tag exists.
  - New `Advisory` fields: `Description string`, `CVSSVector string`,
    `CVSSScore float64`, `CVEID string`, `Vulns []Vulnerability`,
    `Credits []Credit`.
- Produces, in `source`:
  - `Snapshot.Releases []model.ReleaseBranch`, sorted newest first.
- CVSS precedence: `cvss_severities.cvss_v4`, then `cvss_v3`, then the
  legacy `cvss`. Use the first one with a non-empty vector.

- [ ] **Step 1: Write failing tests.** Extend `fakeGitHub`:
  - The advisory `GHSA-aaaa…` JSON gets a `description`, a
    `cvss_severities.cvss_v3` (and also a `cvss_v4`, to show v4 wins), a
    `cve_id`, two `vulnerabilities` (one with `patched_versions`, one with
    only `first_patched_version`) and two `credits_detailed`.
  - Add handlers for `GET /repos/example/upstream/git/matching-refs/heads/release-`
    (returning `release-3.7`, `release-4.0`, `release-4.1`, `release-4.2` and
    `release-notes`, which must be ignored) and for `…/matching-refs/tags/v`
    (`v4.1.0`, `v4.0.0`, `v3.7.0`, `v4.2.0-rc1`).

  `TestFetchAdvisoryFields` asserts every new field. `TestFetchReleases`
  asserts `Releases == [{release-4.2 4 2 false} {release-4.1 4 1 true}
  {release-4.0 4 0 true} {release-3.7 3 7 true}]`. A 500 on matching-refs
  makes `Snapshot.Err` non-nil.
- [ ] **Step 2:** `go test ./internal/source/` fails.
- [ ] **Step 3: Implement.** Map the fields in `advisory()`. Add
  `func (s *Source) releases(ctx context.Context) ([]model.ReleaseBranch, error)`,
  called once per `fetchAll` through `s.c.Git.ListMatchingRefs(ctx, owner,
  repo, "heads/release-")` and `"tags/v"`. Parse names with
  `versions.Parse` on `X.Y.0`, and skip names that don't parse. Run it in
  the same errgroup as the advisories.
- [ ] **Step 4:** `go test ./internal/source/` passes. `make check` passes.
- [ ] **Step 5:** commit `feat(source): read advisory details and the upstream's release branches`.

### Task 3: Checklist rules and their state

**Files:**
- Create: `internal/model/checklist.go`
- Test: `internal/model/checklist_test.go`
- Modify: `internal/model/status.go` (the `ChecklistOpen` status)
- Modify: `internal/state/state.go` (the `Checks` map); test in `internal/state/state_test.go`

**Interfaces:**
- Consumes: `versions.AnyCoversMinor` (Task 1); the Task 2 model types.
- Produces:
  ```go
  type Item string
  const (
      ItemBranches Item = "branches"; ItemDescription Item = "description"; ItemCVSS Item = "cvss"
      ItemCVE Item = "cve"; ItemVersions Item = "versions"; ItemCredits Item = "credits"
  )
  var Items = []Item{ItemBranches, ItemDescription, ItemCVSS, ItemCVE, ItemVersions, ItemCredits}

  type Tick struct {
      At   time.Time `json:"at"`
      Hash string    `json:"hash,omitempty"` // "" for description: never stale
  }
  // Checks is one advisory's local checklist record.
  type Checks struct {
      Ticks        map[Item]Tick        `json:"ticks,omitempty"`
      Waived       map[string]time.Time `json:"waived,omitempty"` // an Item, or "branch:<name>"
      CVERequested time.Time            `json:"cve_requested,omitzero"`
  }

  type ItemState int // ItemOpen, ItemDone, ItemWaived, ItemStale
  type BranchLine struct { Branch string; PR int /* 0 = missing */; Waived bool }
  type ItemResult struct {
      Item     Item
      State    ItemState
      Note     string       // e.g. "can't read affected versions", "not set"
      Branches []BranchLine // ItemBranches only: expected branches, then unexpected ones with a PR
      Expected map[string]bool // ItemBranches only
      Hints    []Credit     // ItemCredits only: suggested, missing credits
  }
  type Checklist struct{ Results []ItemResult }

  func ValueHash(a Advisory, it Item) string
  func Evaluate(a Advisory, rel []ReleaseBranch, supported int, c Checks) Checklist
  func (c Checklist) Count() (done, total int) // done counts ItemDone and ItemWaived
  func (c Checklist) Complete() bool
  func (c *Checks) Toggle(a Advisory, rel []ReleaseBranch, supported int, it Item, branch string, now time.Time)
  func (c *Checks) MarkCVERequested(now time.Time)
  func (a Advisory) StatusWith(cl Checklist) Status // Ready becomes ChecklistOpen when !cl.Complete()
  ```
  - Add `ChecklistOpen` to `Status`, after `Ready` in the `const` block.
    (It can't be called `Checklist`, which is the type above.) Its
    `String()` is `"checklist"`, and render adds the count.
  - In `state`: `State.Checks map[string]model.Checks \`json:"checks"\``,
    initialised in `Empty` and `Load` like the other maps.

**The rules** (copied from the spec):
- **branches:** done when every expected branch has an open fix PR or a
  `branch:<name>` waiver.
  - If `a.Vulns` is empty, the item is open with Note "no affected versions
    set".
  - If a range doesn't parse, the item is open with Note "can't read
    affected versions".
  - A waiver on a branch that isn't expected is ignored.
- **description, cvss, versions, credits:**
  - done when ticked and `Tick.Hash == ValueHash(a, it)`;
  - stale when ticked with a different hash;
  - waived when the waiver key is set;
  - otherwise open, with Note "not set" when the value is empty.
- **cve:** done when `a.CVEID != ""` or `!c.CVERequested.IsZero()`.
- **ValueHash:** the sha256 hex of a canonical string, sorted before
  hashing, never including the text itself:
  - cvss: the vector;
  - versions: the sorted `package|range|patched` lines;
  - credits: the sorted `user|type` lines (the state is left out);
  - description: always `""`.
- **Credit hints:** the fix PRs' authors as `remediation_developer`, and
  anyone whose latest review approves a PR as `remediation_reviewer`, when
  that user doesn't already have that credit type. Don't repeat the same
  hint.
- **Toggle**, the semantics of `space`:
  - branches with `branch != ""`: toggles that branch's waiver, but only if
    the branch is expected and has no PR.
  - cve: toggles `CVERequested`. It does nothing when `CVEID` is set.
  - other items:
    - ticked (done or stale): retick with the current hash if stale,
      otherwise untick;
    - waived: unwaive;
    - value empty: waive;
    - otherwise: tick with the current hash.

    For description, an empty value is waived and a set one is ticked
    with `Hash: ""`.

- [ ] **Step 1: Write failing tests** in `checklist_test.go`. Use the
  releases from Task 2's test (4.2 RC, 4.1, 4.0, 3.7) and `supported=2`.
  - `TestExpectedBranches`:
    - `>= 4.0.0, < 4.0.7` expects `main`, `release-4.0`, `release-4.2` (RC
      only), not `release-4.1`;
    - `< 3.7.5` expects `main`, `release-4.2` (3.7 isn't among the newest
      two GA branches);
    - `< 4.1.3` with `supported=3` expects `main`, `release-4.0`,
      `release-4.1`, `release-4.2`;
    - an unparseable range gives an open item with the note.
  - `TestBranchesDone`:
    - PRs on `main` and `release-4.0`, plus a waiver on `release-4.2`:
      done;
    - drop the waiver: open;
    - a waiver on `release-4.1` (not expected) doesn't count;
    - a PR to an unexpected branch is listed and doesn't block.
  - `TestTickStale`:
    - tick cvss, change the vector: stale;
    - tick versions, reverse `a.Vulns`: still done (Review Focus #2);
    - tick credits, change a credit's state from pending to accepted:
      still done;
    - add a credit: stale.
  - `TestDescriptionNeverStale`: tick it, change the description: done.
  - `TestCVE`: `CVEID` set means done; `CVERequested` set means done; both
    empty means open.
  - `TestToggle` covers each Toggle rule above, including that waiving
    happens only on an empty value.
  - `TestCreditHints`: PR author `fixer` with no credits gives
    `{fixer remediation_developer}`; an approver gives
    `remediation_reviewer`; an existing matching credit gives no hint.
  - `TestStatusWith`: a Ready advisory with 5/6 gives `ChecklistOpen`; with
    6/6 it gives `Ready`; a NeedsCI advisory stays `NeedsCI`.

  In `state_test.go`, `TestLoadOldStateHasChecks`: a file without `checks`
  loads with a non-nil `Checks`, and an `Update` that sets a tick
  round-trips at mode `0600` (Review Focus #3).
- [ ] **Step 2:** `go test ./internal/model/ ./internal/state/` fails.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test ./internal/model/ ./internal/state/` passes.
  `make check` passes.
- [ ] **Step 5:** commit `feat(model): the release checklist's rules and local record`.

### Task 4: Config for the checkout and supported branches

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `cmd/root.go`, `cmd/status.go` (callers)
- Modify: `README.md` (Setup section: the two new keys)

**Interfaces:**
- Produces:
  ```go
  type Config struct {
      Repos             gh.Repos
      Checkout          string // absolute; default $HOME/sources/<upstream name>-security
      SupportedBranches int    // default 2
  }
  func Load(path, upstream, mirror string) (Config, error)
  ```
  - JSON keys: `checkout`, `supported_branches`.
  - A leading `~/` in `checkout` expands to `$HOME`.
  - `supported_branches < 1` is an error.
  - The default `<upstream name>-security` gives `argo-workflows-security`
    for the spec's case without compiling in a repo name, which the config
    package says it never does.

- [ ] **Step 1: Write failing tests.** `TestLoadDefaults`: with
  `{"upstream":"o/argo-workflows","mirror":"o/m"}` and `HOME=/h`, the result
  is `Checkout == "/h/sources/argo-workflows-security"` and
  `SupportedBranches == 2`. `TestLoadCheckoutTilde`: `~/x` becomes `/h/x`.
  `TestLoadSupportedBranchesInvalid`: `0` is an error. Update the existing
  tests to read `.Repos`.
- [ ] **Step 2:** `go test ./internal/config/` fails.
- [ ] **Step 3:** Implement, and update `cmd/root.go` and `cmd/status.go`
  to use `cfg.Repos`.
- [ ] **Step 4:** `make check` passes.
- [ ] **Step 5:** commit `feat(config): configure the security checkout and supported branches`.

### Task 5: Show the count and status word

**Files:**
- Modify: `internal/render/plain.go`, `internal/render/text.go`, `internal/render/plain_test.go`
- Modify: `internal/tui/view.go`, `internal/tui/view_test.go`, `internal/tui/model.go` (`Deps.Supported int`)
- Modify: `cmd/status.go`, `cmd/root.go`

**Interfaces:**
- Consumes: `model.Evaluate`, `Checklist.Count`, `Advisory.StatusWith` (Task 3);
  `Snapshot.Releases` (Task 2); `Config.SupportedBranches` (Task 4).
- Produces:
  - `func render.Checklists(snap source.Snapshot, checks map[string]model.Checks, supported int) map[string]model.Checklist`,
    keyed by GHSA ID.
  - `func render.StatusText(a model.Advisory, cl model.Checklist) string`:
    `"checklist 3/6"` for `ChecklistOpen`, otherwise `StatusWith(cl).String()`.
  - `func render.CountText(cl model.Checklist) string`: `"3/6"`.
  - `render.Plain(w io.Writer, snap source.Snapshot, queue []string, lists map[string]model.Checklist) error`.
  - A new `REL` column of width 4 after STATE, in both the plain and TUI
    layouts. In the TUI it's green at 6/6, otherwise the warn style. The
    `ChecklistOpen` status uses the warn style.

- [ ] **Step 1: Write failing tests.** `TestPlainChecklist`: a Ready
  advisory (green and approved on its PRs) with no ticks prints
  `checklist 1/6`. Only cve is done here, through `CVEID`. The REL column
  shows `1/6`. A NeedsCI advisory prints `needs CI` and still shows its
  count. Update the existing plain and view golden strings for the new
  column.
- [ ] **Step 2:** `go test ./internal/render/ ./internal/tui/` fails.
- [ ] **Step 3: Implement.** In the TUI, `View` computes the lists once per
  render from `m.loadState().Checks`. `cmd/status.go` passes
  `st.Checks` and `cfg.SupportedBranches`.
- [ ] **Step 4:** `make check` passes.
- [ ] **Step 5:** commit `feat(render): show each advisory's checklist count`.

### Task 6: Requesting a CVE

**Files:**
- Create: `internal/cve/cve.go`, `internal/cve/cve_test.go`
- Create: `cmd/requestcve.go`
- Modify: `cmd/root.go` (add the subcommand)

**Interfaces:**
- Consumes: `model.Checks.MarkCVERequested` (Task 3); `state.Update`.
- Produces:
  - `func cve.Request(ctx context.Context, c *github.Client, upstream gh.Repo, statePath, ghsa string, now time.Time) error`.
    It calls `c.SecurityAdvisories.RequestCVE`. Only on success does it
    record `Checks[ghsa].MarkCVERequested(now)` under `state.Update`. An
    API error is wrapped as `requesting a CVE for <ghsa>: …` and records
    nothing.
  - `ghsa-manager request-cve <GHSA>`: exactly one argument, normalised
    with `model.NormalizeID`. It prints `requested a CVE for <GHSA>`.

- [ ] **Step 1: Write failing tests** against an httptest server:
  - `TestRequestRecords`: a 202 from `POST /repos/o/r/security-advisories/GHSA-x/cve`
    sets `CVERequested` in the state file.
  - `TestRequestFailsRecordsNothing`: a 422 returns an error, and the
    state has no record.
- [ ] **Step 2:** `go test ./internal/cve/` fails.
- [ ] **Step 3:** Implement the package and subcommand.
- [ ] **Step 4:** `make check` passes.
- [ ] **Step 5:** commit `feat: request a CVE and record it`.

### Task 7: The checklist screen

**Files:**
- Create: `internal/tui/checklist.go` (keys and view for the screen)
- Test: `internal/tui/checklist_test.go`
- Modify: `internal/tui/model.go` (screen mode fields, `Deps.RequestCVE`), `internal/tui/keys.go` (`l`), `internal/tui/view.go` (dispatch)
- Modify: `cmd/root.go` (wire `RequestCVE` to `cve.Request`), `README.md` (keys)

**Interfaces:**
- Consumes: Tasks 3, 5 and 6.
- Produces:
  - `Deps.RequestCVE func(ctx context.Context, ghsa string) error`
  - `Model` fields: `listID string` (the advisory on the checklist screen;
    `""` means the main screen), `listCursor int`, `confirmCVE bool`.
  - `func (m Model) listLines(cl model.Checklist) []listLine`, where
    `type listLine struct{ item model.Item; branch string }`. The lines go
    in `model.Items` order. The branches item is one header line
    (`branch == ""`), then one line per `BranchLine`.

**Behaviour:**
- `l` on any row of an advisory opens its checklist screen with the cursor
  on the first line.
- Each line shows `✓` (done), `✗` (open), `~` (waived) or `!` (stale,
  "ticked (changed since)"), then the item's label and its detail:
  - branches: `main #1 ✓ · release-4.0 missing`, with each branch on its
    own line below;
  - description: `ticked <date>` or `not ticked`;
  - cvss: `<score> <vector>` or `not set`;
  - cve: `<CVE ID>`, `requested <date>` or `not requested`;
  - versions: `<range> → <patched>`, one line per vulnerability;
  - credits: `<user> (<type>, <state>)` entries, then `suggest: <user> as
    <type>` hints, dimmed.

  Dates use `2 Jan`.
- `j`/`k`/up/down move the cursor. `space` calls `Checks.Toggle` inside
  `m.updateState` for the line's item and branch. `C` asks
  `request a CVE for <GHSA>? y/n`; `y` runs `Deps.RequestCVE` as a `tea.Cmd`
  and shows its result as a note. `a` opens the session (Task 8; until
  then, ignore the key). `esc` and `q` go back to the main screen. `q`
  doesn't quit from this screen.
- If the advisory disappears from a fresh snapshot, go back to the main
  screen with the note `<GHSA> is no longer a draft`.
- The footer's help line changes to
  `space tick/untick · C request CVE · a Claude · esc back`.

- [ ] **Step 1: Write failing tests** in `checklist_test.go`, using the
  existing `fakes` and `newModel`:
  - `TestChecklistOpensAndCloses`: `l` then `esc` is back on the main
    screen, and `q` on the checklist screen doesn't return `tea.Quit`.
  - `TestChecklistTickPersists`: the cursor on description plus `space`
    puts a tick for `idA` in the state file, and the view shows `✓`. A
    second `space` removes it.
  - `TestChecklistWaiveBranch`: the cursor on the missing `release-4.2`
    line plus `space` writes the `branch:release-4.2` waiver. `release-4.2`
    has only an RC, so it's always expected, and `idA` has no PR to it.
  - `TestChecklistRequestCVE`: `C` then `n` doesn't call the fake. `C` then
    `y` calls it with `idA`.
  - `TestChecklistVanished`: a snapshot without `idA` returns to the main
    screen with the note.
  - `TestChecklistStale`: a cvss tick with an old hash renders
    `ticked (changed since)`.

  Extend `snapshot()` in `model_test.go` with `Vulns`, a CVSS vector and
  `Snapshot.Releases` as needed.
- [ ] **Step 2:** `go test ./internal/tui/` fails.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `make check` passes. Run `make build && bin/ghsa-manager`
  to check the screen against mockup C (spec, Goals 4).
- [ ] **Step 5:** commit `feat(tui): a checklist screen per advisory`.

### Task 8: The Claude session

**Files:**
- Create: `internal/session/session.go`, `internal/session/session_test.go`
- Modify: `internal/tui/model.go` (`Deps.OpenSession`), `internal/tui/keys.go` and `internal/tui/checklist.go` (`a`)
- Modify: `cmd/root.go` (wire it), `go.mod`/`go.sum` (`github.com/google/uuid`)
- Modify: `CLAUDE.md` (security exception), `README.md` (the `a` key, what the session can do)

**Interfaces:**
- Consumes: `Config.Checkout` (Task 4); `model.Checklist` (Task 3).
- Produces:
  ```go
  // Run runs a command in dir and returns its stdout.
  type Run func(ctx context.Context, dir, name string, args ...string) (string, error)
  type Opener struct {
      Run        Run
      Upstream   gh.Repo
      Checkout   string
      ClaudeHome string // ~/.claude, for finding session transcripts
      Self       string // os.Executable(), quoted in the prompt for request-cve
      InTmux     bool   // os.Getenv("TMUX") != ""
  }
  var ErrNoTmux = errors.New("not inside tmux: run ghsa-manager in tmux to open a Claude session")
  func SessionID(upstream gh.Repo, ghsa string) string // uuid.NewSHA1(namespace, []byte(upstream.String()+"/"+ghsa))
  func (o *Opener) Open(ctx context.Context, a model.Advisory, open []model.Item) (string, error) // returns the note to show
  func Prompt(o *Opener, a model.Advisory, open []model.Item) string
  ```
  - `namespace` is a fixed UUID constant of this package's own; generate it
    once and hard-code it.
  - `Deps.OpenSession func(ctx context.Context, a model.Advisory, open []model.Item) (string, error)`.

**Open, step by step** (copied from todaydeck's `OpenProject`):
1. If `!InTmux`, return `ErrNoTmux`.
2. Run `tmux list-windows -a -F '#{window_id} #{@ghsa_key}'`. If a line's
   key equals `a.ID`, run `tmux switch-client -t <window_id>` and return
   `Switched to <GHSA>`.
3. If `Checkout` isn't a directory, return the error `the checkout <path>
   is missing; set "checkout" in the config`. Never create it.
4. Build the claude arguments: `claude --strict-mcp-config --mcp-config
   '{"mcpServers":{}}'`, then:
   - `--resume <id>` when `ClaudeHome/projects/*/<id>.jsonl` exists, or
   - `--session-id <id> <Prompt(...)>` otherwise.
5. Run `tmux new-window -P -F '#{window_id}' -n <GHSA> -c <Checkout>
   <shell-quoted command>`. Then run `tmux set-option -w -t <id> @ghsa_key
   <GHSA>`. Return `Started Claude on <GHSA>` or `Resumed Claude on <GHSA>`.
   If `claude` isn't on PATH, check with `exec.LookPath` in the `cmd`
   wiring before tmux runs, and return a clear error.

**Prompt** contains, in plain sentences:
- the GHSA ID, the upstream, the advisory link, the fork and each fix PR's
  link and base;
- the open item names;
- these rules:
  - read the advisory with `gh api repos/<upstream>/security-advisories/<GHSA>`;
  - change it only with `gh api -X PATCH` on the same path, and only after
    Alan agrees in this chat;
  - request a CVE only with `<Self> request-cve <GHSA>`;
  - never post advisory comments (the reporter sees them and they can't be
    deleted), publish the advisory, or change its state;
  - never put advisory details anywhere outside GitHub's private surfaces
    and this session;
  - work only in this checkout, and never push to the public upstream.

The prompt ends by asking the session to get its bearings, then ask Alan
what to work on. It must not contain `a.Summary` or `a.Description`.

- [ ] **Step 1: Write failing tests** with a fake `Run` that records calls,
  as todaydeck's `workspace_test.go` does:
  - `TestOpenStarts`:
    - no window and no transcript;
    - the `new-window` call has `-n GHSA-aaaa-bbbb-cccc -c <checkout>`;
    - the command contains `'--strict-mcp-config'`, `'--mcp-config'
      '{"mcpServers":{}}'` and `'--session-id' '<SessionID>'`;
    - the last call is `tmux set-option -w -t @7 @ghsa_key GHSA-aaaa-bbbb-cccc`.
  - `TestOpenResumes`: with a transcript present, the command has
    `'--resume' '<id>'` and still has `--strict-mcp-config`.
  - `TestOpenSwitches`: when `list-windows` returns
    `@4 GHSA-aaaa-bbbb-cccc`, the only further call is
    `tmux switch-client -t @4`.
  - `TestOpenNoTmux` returns `ErrNoTmux` with no calls.
    `TestOpenNoCheckout` returns an error, makes no `new-window` call, and
    leaves the directory missing.
  - `TestPromptKeepsDetailsOut`: an advisory with Summary `SECRET-TITLE` and
    Description `SECRET-BODY` gives a prompt and command line with neither
    string, but with the GHSA ID, the fork and `request-cve`.
  - `TestSessionIDStable`: same inputs give the same ID; a different GHSA
    gives a different ID; the ID parses as a UUID.

  In `internal/tui`, `TestSessionKey`: `a` on the main screen and on the
  checklist screen both call the fake `OpenSession` with the advisory and
  its open items, and its returned string or error shows as a note.
- [ ] **Step 2:** `go test ./internal/session/ ./internal/tui/` fails.
- [ ] **Step 3: Implement.** Run `go get github.com/google/uuid`. Wire
  `Opener` in `cmd/root.go` with an `exec.CommandContext` runner, and
  `ClaudeHome` from `$HOME/.claude`.
- [ ] **Step 4: Docs.** In CLAUDE.md, under Security rules, change the first
  bullet to:
  `Only talk to api.github.com, github.com, and git over SSH to the private mirror (configured as mirror). No other services, no telemetry. The one exception is the interactive Claude Code session that a opens, which Alan has chosen to allow; it runs with no MCP servers.`
  In the README, add `a` and `l` to the Keys line, and add a sentence on
  the session and `request-cve`.
- [ ] **Step 5:** `make check` passes.
- [ ] **Step 6: Manual check (Review Focus #5).** `make build`, then run
  `bin/ghsa-manager` inside tmux and press `a` on an advisory:
  - the window is named with the GHSA ID only;
  - `/mcp` in the session lists **no** servers, with no claude.ai
    connectors and no plugin servers;
  - pressing `a` again switches to the window;
  - after closing the window, pressing `a` resumes the same session.

  If `/mcp` isn't empty, stop and report back to Alan before committing.
- [ ] **Step 7:** commit `feat: open a Claude session per advisory in tmux`.
