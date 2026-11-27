# Release checklist and a Claude session per advisory: design

## Why

Besides CI and review, every draft advisory needs some other work before it
can be published. Alan currently keeps track of that work in his head:

- fix PRs to the right branches;
- a correct description;
- a CVSS score;
- a CVE requested;
- correct affected versions.

None of these is hard. Some are mechanical, like the affected versions, and
some are judgement calls. The goal is to keep them in the tool, so the
screen shows which items are done and which still need doing. *(Alan)*

Alan also wants to open an interactive Claude Code session on an advisory,
to help with these items. It should open in a tmux window, the way
todaydeck opens work on a PR. *(Alan)*

This builds on `2026-11-26-ghsa-manager-design.md`. Everything in that
document still applies unless this one changes it.

## Background the builder needs

- **The advisory's fields** come from the same draft-advisory listing the tool
  already fetches (`GET /repos/{owner}/{repo}/security-advisories?state=draft`):
  - `description`;
  - `cvss` / `cvss_severities` (vector and score);
  - `cve_id`;
  - `vulnerabilities[]`, each with `package`, `vulnerable_version_range`
    (for example `>= 4.0.0, < 4.0.7` or `< 3.7.0`) and `patched_versions`.

  go-github's `SecurityAdvisory` type already has all of these.
- **Editing an advisory:** `PATCH /repos/{owner}/{repo}/security-advisories/{ghsa_id}`
  changes the summary, description, severity, CVSS vector, CWEs, CVE ID and
  vulnerabilities. go-github doesn't wrap it, but `gh api` can call it.
- **Requesting a CVE:** `POST …/security-advisories/{ghsa_id}/cve` (go-github
  `RequestCVE`). The API has **no field for a CVE that has been requested but
  not yet assigned**. `cve_id` stays null until GitHub assigns one. The tool
  can only know about a request if it made the request itself, or if Alan or the
  session says so.
- **todaydeck's pattern**, to copy (`../todaydeck/internal/workspace/`):
  - each item of work gets one tmux window, tagged with a window option
    (`@deck_key`);
  - opening it again runs `tmux switch-client` to the window if it's still
    open;
  - otherwise it runs `claude --resume <id>` if the session exists, or
    `claude --session-id <id> <prompt>` to start a new one;
  - the session ID is a UUIDv5 of the work's key, so the same key always
    gets the same session;
  - it refuses with a clear error when it isn't running inside tmux.
- **Supported branches:** argo-workflows supports `main` and the two newest
  `release-X.Y` branches.

## Goals

1. **Five checklist items per draft advisory:**
   - **PRs to branches:** each expected branch has an open fix PR. Ticked by
     the tool.
   - **Description:** ticked by Alan. *(Alan: judgement call)*
   - **CVSS score:** the screen shows the vector and score once set, but it
     counts only when Alan ticks it. *(Alan: "usually they're not right at
     first")*
   - **CVE requested:** done when the advisory has a CVE ID, or once a request
     has been recorded. A request still waiting for an ID counts as done.
     *(Alan)*
   - **Affected versions:** the screen shows the ranges and patched versions
     once set, but they count only when Alan ticks them. *(Alan)*

   The tool ticks only what it can check for certain. *(Alan)*
2. **Main screen:** a new short column on each advisory row, `n/5`, green at
   5/5. Nothing else on the main screen changes. *(Alan: layout C, so the main
   screen stays short)*
3. **Status word:** an advisory that would be **ready** shows
   **checklist n/5** until all five items are done. The earlier status words
   (no fork, needs CI, needs review) come first, unchanged.
4. **Checklist screen:** `l` on an advisory opens a screen with one line per
   item, showing what's done and the detail behind each item:
   - *PRs to branches:* each expected branch, with its PR number or "missing".
   - *Description:* who ticked it and when.
   - *CVSS:* the vector and score, or "not set", and whether it's ticked.
   - *CVE:* the CVE ID, "requested <date>", or "not requested".
   - *Affected versions:* each vulnerable range and its patched version, and
     whether it's ticked.

   Its keys:
   - `space` ticks or unticks the item under the cursor;
   - a key that requests a CVE, after a y/n confirmation;
   - `a` opens the Claude session;
   - `esc` goes back.

   (Mockup option C, agreed with Alan.)
5. **A Claude session per advisory:** `a` on the main screen or the checklist
   screen opens an interactive Claude Code session about that advisory, in a
   tmux window, the way todaydeck does it:
   - one window per advisory, tagged with its GHSA ID;
   - pressing `a` again switches to the window if it's open, resumes the
     session if it isn't, or starts a new one if there is none.

   The session helps with the checklist. Once Alan agrees in the chat, it can
   change the advisory on GitHub through the PATCH API. *(Alan: "if there is an
   API, it can change it")*
6. **`ghsa-manager status`** prints the `n/5` count too.

## Non-goals

- Publishing the advisory, or changing its state. This is still drafts-only.
- Checking that the description, CVSS or versions are *correct*. Only Alan
  decides that.
- The session working unattended or reporting back by itself. It's
  interactive, with Alan in it. *(Alan)*
- Checklist items beyond these five. They can be added later.

## Anti-goals

- **Leaking advisory details.** Ticks, waivers and request records stay in the
  local state file. The tmux window name is the GHSA ID, never the title,
  because window names show up in tmux's lists and in todaydeck's
  other-windows pane. No advisory text goes in the command line except the
  GHSA ID, the fork name and the links.
- **A tick that hides a later change.** If CVSS or the versions change after
  Alan ticked them, the old tick mustn't keep counting (see Claude's calls).
- **The session acting outside the advisory's private surfaces** without
  Alan's say-so. In particular, it mustn't post advisory comments, because the
  reporter can see them and they can't be deleted. The prompt says so, and
  there are no MCP servers in the session, but `gh api` is open by Alan's
  choice. These rules depend on the prompt.
- **Embargoed work in a public checkout.** The session never runs in a
  checkout used for public work, so its memory, transcripts and any fork code
  stay apart from public sessions.

## Decided

- The tool ticks deterministic items. Alan ticks description, CVSS and
  affected versions. *(Alan)*
- A requested CVE counts as done. *(Alan)*
- The checklist screen is behind a key, and the main screen gets only a
  count. *(Alan, option C)*
- The Claude session is interactive, in tmux, modelled on todaydeck. *(Alan)*
- The session may change the advisory through the API once Alan has agreed
  in the chat. *(Alan)*
- **Claude is the one exception to "only talk to GitHub".** Running the
  session is OK, and it sends advisory details to Anthropic. *(Alan)* As part
  of this work, CLAUDE.md's security rules gain a line saying so: the only
  exception is the interactive session this tool launches. The tool itself
  still talks only to GitHub and the mirror.
- **The session loads no MCP servers.** It's launched with
  `--strict-mcp-config` and an empty MCP config, so none of Alan's usual
  servers or connectors are available in it. *(Alan)*
- **`gh api` isn't blocked.** The session may use `gh api` freely. The rules
  in the starting prompt (no advisory comments, no state changes) are
  guidance, not enforced. *(Alan: "allow gh api calls though")*
- **The session runs in `~/sources/argo-workflows-security`,** Alan's
  dedicated checkout for embargoed work, with the GHSA fork remotes. It never
  runs in the checkout he uses for public work. *(Alan)*

## Claude's calls (Alan can override)

- **Expected branches:** `main`, plus every supported `release-X.Y` branch
  whose minor version falls in a vulnerable range. A branch counts as
  affected when some version `X.Y.z` is inside the range.
  - "Supported" means the newest N upstream `release-*` branches, where N is
    a config setting (`supported_branches`) defaulting to 2.
  - If there are no ranges, or a range can't be parsed, the item shows that
    instead of guessing, and it isn't done.
  - A fix PR to a branch that isn't expected is shown, and doesn't count
    against the item.
  - **Release branches with only pre-releases** (an RC tag but no `vX.Y.0`
    tag) are always expected, because they need the fix before release.
    They don't count towards N. "The newest N" means the newest N branches
    that have a GA release.
- **Waivers:** `space` on an item the tool checks waives it. A waived item
  shows as "waived", not "done", but counts towards n/5. `space` again
  removes the waiver.
  - For *PRs to branches*, waivers are **per branch**. Each expected branch
    gets its own line on the checklist screen for the cursor, and `space` on
    a missing branch waives just that branch. The item is done when every
    expected branch has a PR or a waiver. A branch that becomes expected later
    shows as missing. It isn't covered by an earlier waiver.
  - The CVE item can't be waived. `space` on it marks it requested.
- **Ticks go stale:** a tick on CVSS or affected versions records a hash of
  the value Alan ticked. If the value changes, the item shows "ticked
  (changed since)" and stops counting, the way approvals go stale. A
  description tick doesn't go stale, because descriptions get small wording
  edits. Only hashes go in the state file, never the text.
- **Ticking an empty item:** you can't tick CVSS or affected versions while
  they're not set. You can waive them.
- **Recording CVE requests:** add a `ghsa-manager request-cve <GHSA>`
  subcommand. It calls the API and records the request in local state. The
  checklist screen's CVE key uses the same code. The session is told to use
  this subcommand, so its requests are recorded too. For a request made in
  GitHub's web page, Alan presses `space` on the CVE item to mark it
  requested.
- **Where the session runs** is a new config setting, `checkout`, defaulting
  to `~/sources/argo-workflows-security` (see Decided). The tool doesn't
  create the directory or change it. If it's missing, opening the session
  refuses with a note. The session can look at the fork's PRs with `gh`, and
  add the fork as a git remote there if it needs the code. The tool doesn't
  do that itself.
- **The tmux tag:** the window option `@ghsa_key`, set to the GHSA ID. The
  session ID is a UUIDv5 of the upstream and the GHSA ID, under a namespace
  constant of the tool's own, not todaydeck's.
- **The starting prompt** gives:
  - the GHSA ID, the upstream, the advisory's link, the fork and its fix PRs'
    links;
  - which checklist items are open;
  - the rules:
    - read the advisory itself with `gh api`;
    - change the advisory only through the PATCH API, and only after Alan
      agrees in the chat;
    - request a CVE only through `ghsa-manager request-cve`;
    - never post advisory comments, publish, or change the advisory's state;
    - never put advisory details anywhere outside GitHub's private surfaces
      and this session;
    - work only in this checkout, and never push to the public upstream.

  Then it asks the session to get its bearings and ask Alan what to work on.
  Since sessions resume, the prompt is only sent the first time.
- **Refreshing:** after a tick, waiver or CVE request, the screen updates
  straight away. Changes the session makes on GitHub show up at the next
  fetch, or with `r`.

## Left to the builder

- The checklist screen's exact layout, colours and cursor movement, and the
  CVE-request key. Stay close to mockup C.
- How a range like `>= 4.0.0, < 4.0.7` is parsed. Use a small hand-written
  parser or a library already in the module graph, and test it on real
  advisory ranges, including several ranges and open-ended ones.
- Where the new state lives in the state file, keeping the file's existing
  locking and atomic writes.
- How the tmux and claude commands are run and tested. todaydeck injects
  a command runner and fakes it in tests.
