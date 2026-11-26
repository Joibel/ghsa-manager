# ghsa-manager: design

## Why

Alan maintains argoproj/argo-workflows' draft security advisories (GHSAs). Today, keeping track of them is manual. He opens each advisory to check:

- whether it has a temporary private fork (and creates one if not);
- whether the fork has fix PRs, and for which branches;
- whether CI has run on them;
- whether anyone has reviewed them.

There are around 20 drafts at a time, and the manual round is slow and easy to get wrong. This tool replaces that round with one screen, and runs CI on request without flooding the shared runners.

The tool is kept separate from Alan's other tools (cherry-picker, todaydeck) because everything it touches is embargoed security work. It will feed into todaydeck later, but not as part of this design.

## Background the builder needs

- **Draft advisories** are listed with `GET /repos/argoproj/argo-workflows/security-advisories?state=draft`. Each advisory's temporary private fork is in `.private_fork`, which is null if there's no fork yet. A fork is created with `POST /repos/argoproj/argo-workflows/security-advisories/{ghsa_id}/forks`.
- **Fix PRs** are ordinary PRs inside the fork (`gh pr list -R <fork>`). There's usually one per target branch: `main`, `release-4.1`, `release-4.0`. Their base branches have the same names as upstream's.
- **Forks can't run GitHub Actions**, and no integration (GitHub Apps, `GITHUB_TOKEN`) can read them. Only a user's token can. All reads use Alan's `gh` login.
- **CI runs in a private mirror**, the private mirror (configured as `mirror`).
  - The script `ghsa-ci.sh` lives on the mirror's `mirror-sync` branch. Its README there explains it in full; read both.
  - It copies a fork PR's head to a mirror branch named `ghsa-<id>/<base>-pr<N>`, fast-forwards the mirror's base branch, and opens or updates a mirror PR, where the `CI` workflow (`ci-build.yaml`) runs.
  - It refuses to push if any upstream workflow other than CI and Docs is enabled in the mirror. It also refuses if CI or Docs reference unexpected secrets, or if harden-runner isn't disabled.
  - It pushes over SSH with the user's key.
- **Known gap in `ghsa-ci.sh`:** when the fix head is unchanged but the base has moved, re-pushing the same commit sends GitHub no PR event, so CI doesn't restart. The script still prints "CI restarts there". Closing and reopening the mirror PR forces a fresh run, but only after GitHub has recomputed `refs/pull/<n>/merge` against the new base. Reopen too early and the run silently tests the old base. This was hit three times on 2026-11-26.
- **Reporting back:** the repository security advisory comments API (`GET/POST …/security-advisories/{ghsa_id}/comments`, `PATCH …/comments/{id}`) works with Alan's token. A test comment on a draft advisory created no notifications, and editing it in place worked.
  - Comments are visible to the advisory's reporter and can't be deleted.
  - That hand-posted test comment starts with "**CI status for the fix PRs**", so the tool adopts it rather than adding a second.
- **Runners** are shared CNCF runners and shouldn't be flooded. A full CI run is about 38 jobs, about 30 of them e2e.
- **Pushes need a YubiKey touch**, one per SSH push. Alan has decided to keep it that way for now. The tool runs interactively, with him at the screen.

## Goals

1. **One main screen** (layout "A", agreed from a mockup). It lists every draft GHSA, grouped, with one row per open fix PR. Each GHSA shows:
   - its ID and whether it has a fork;
   - a status word for what it needs next;
   - its queue position, or none if it isn't queued.

   Each PR row shows:
   - target branch and PR number;
   - last CI run: result (green, red, running or cancelled) and how long ago. It also says "never" if CI hasn't run, or "stale" when the fork PR's head has moved since that run. A green run whose unit or e2e test jobs were skipped shows which ones;
   - reviews: every reviewer's name and whether they approved or requested changes. Any reviewer counts, maintainer or not. A review given on an earlier head shows as stale, for example "approved (stale)". GHSA forks have no branch protection to dismiss old reviews.
2. **A status word per GHSA**, one of:
   - **no fork**;
   - **fork, no PRs**;
   - **needs CI**: some PR has no run on its current head, or its run isn't green;
   - **needs review**: CI is green on every PR, but some PR has no approval on its current head;
   - **ready**: every PR has green CI on its current head and at least one approval given on its current head.

   CI and review can happen in either order, so a PR can be approved with no CI yet; it still shows "needs CI". A change request counts against "ready" only if that reviewer's latest review is a change request on the current head. Stale approvals and stale change requests are shown, but don't count either way. *(Alan: approvals go stale.)*
3. **Actions from the screen:**
   - mark a GHSA for CI, which adds it to the end of the queue;
   - unmark it;
   - move it up or down the queue;
   - open the advisory or a PR in the browser;
   - make a fork for a GHSA without one.
4. **The CI queue:**
   - Marking runs a GHSA's PRs **once**; then it leaves the queue. It doesn't keep watching for changes.
   - It only runs PRs that **don't already have green CI on their current head**. Green ones are skipped. *(Alan)*
   - The queue is ordered by GHSA. Within a GHSA, `main` goes first, but the `release-*` PRs **don't wait for it to go green**: flaky tests mean a red `main` isn't always a real problem. Instead, the screen flags any GHSA whose `main` PR isn't green while its cherry-picks run or are queued. *(Alan)*
   - PRs start as slots free up, in queue order. A GHSA with more PRs than free slots starts what fits, and the rest follow, so a big GHSA never blocks the queue.
   - **At most N CI runs are in flight at once**, where one fix PR is one run. N defaults to 3 and is configurable. Alan wants at least 3.
   - The footer shows runs in flight against the cap, and how many are queued.
5. **Reporting:** when every queued PR of a GHSA has finished, the tool posts the advisory's CI-status comment, or updates it if one exists. It has one table, with a row per fix PR: branch, PR number, result, and a link to the mirror run. The tool finds its own comment again by a marker it puts in the body.
6. **Commands** in the style of cherry-picker: an interactive `status`/TUI command, plus a non-interactive way to print the same data. A `daemon` command is planned for later; see the non-goals.

## Non-goals (for now)

- Creating fix PRs, or anything to do with the review process itself. The tool only reports reviews.
- Running unattended or as a daemon. That comes later, "once we're under control", and needs the push-credential question solved first.
- Integrating with todaydeck.
- Any advisory state other than draft.
- Watching for changes and re-running CI automatically.

## Anti-goals (failure even if it works)

- **Leaking anything about an advisory outside Alan's machine and GitHub's private surfaces.** That covers advisory IDs, titles, details and fix code. So: no external services, no telemetry, nothing pushed anywhere except the private mirror. Logs, caches and the state file stay local and private.
- **Flooding the shared runners.** The cap must hold even if many GHSAs are marked at once, and even across restarts.
- **CI that silently tests the wrong thing.** For example, a run against a stale merge commit (see "Known gap"), or a green result for a run whose e2e was skipped. If the tool can't confirm a run tested the current head on the current base, it shouldn't report it as green.

## Decided

- Lives at `~/sources/ghsa-manager`, as a new local repo. *(Alan)*
- Status data is fetched live from GitHub. The "last CI run" data comes from the mirror's PRs and workflow runs, so CI started outside the tool, such as a manual `ghsa-ci.sh`, still shows up. *(agreed)*
- Pushes stay on SSH with YubiKey touches for now. While a push is waiting for a touch, the screen should say so. *(Alan)*
- Marking means run once, and only for PRs without green CI on their current head. The cap counts CI runs, default 3. *(Alan)*
- Cherry-picks don't wait for `main`; the screen flags it when `main` isn't green. *(Alan)*
- Reviews: any reviewer, shown by name with approved or changes requested. Reviews given on an earlier head are stale and don't count towards "ready". *(Alan)*

## Claude's calls (Alan can override)

- **Stack:** Go, cobra and go-github, reading the token from `gh auth token`, like cherry-picker. Bubble Tea for the TUI.
- **Running CI:** the tool calls `ghsa-ci.sh` from the mirror's `mirror-sync` branch rather than reimplementing it, so the fail-closed safety checks live in one place. As part of this work, `ghsa-ci.sh` gains the reopen step: when only the base moved, wait until the PR's merge ref is built on the new base, then close and reopen the mirror PR.
- **What the cap counts:** every in-flight `CI` workflow run in the mirror, not just ones the tool started. That way manual runs count against it too.
- **Local state:** only marks, queue order and in-flight bookkeeping, in `~/.local/state/ghsa-manager/`, readable only by Alan, outside any git repo.
- **Making forks** is included, because Alan listed it as part of his manual round.
- **What "green" means:** the run's workflow succeeded, and it tested the PR's **current head merged onto the base the mirror had when the run was triggered**.
  - The tool records that base SHA when it triggers a run. It confirms the merge commit the run actually built (from the run's checkout, `Merge <head> into <base>`) matches. That catches the stale-merge-ref problem.
  - A run that can't be confirmed isn't reported as green.
  - A base that moves *after* the run is only a secondary note. It doesn't make the run stale, which would otherwise happen within hours on `main`.
  - Skipped unit or e2e jobs are listed next to the result, as the mirror's GHSA CI report already does.
- **YubiKey prompts:** Alan's keys ask for a touch through the SSH agent, with no terminal prompt; every push in the 2026-11-26 session worked from a non-interactive shell. So the tool runs `ghsa-ci.sh` in the background and shows "touch YubiKey" while a push waits. A run can need up to three touches per PR: a preflight `ls-remote`, a base fast-forward and the push. Reusing the SSH connection in the script to cut this down is welcome.
- **Changing `ghsa-ci.sh`:** Alan controls `mirror-sync` and commits to it directly. Changes the tool needs go there:
  - the reopen step;
  - a machine-readable output mode, instead of parsing `url -> mirror_pr` lines;
  - several PR numbers per invocation;
  - SSH connection reuse.
- **The shared script:** the tool runs `ghsa-ci.sh` from the mirror's `mirror-sync` branch as it stands, the same copy other maintainers use, not a pinned or vendored one. Anything the tool needs from the script, such as the reopen step or machine-readable output, is added to that shared script. Changes must keep the script's existing manual usage working. *(Alan)*

## Left to the builder

- The TUI's key bindings, colours and refresh interval.
- How in-flight runs are tracked and matched back to fork PRs.
- How long to wait for the merge ref, and how to surface a timeout.
- The exact comment marker and format, keeping to one table per advisory.
- The internal package layout and tests. Follow cherry-picker's conventions where they fit.
