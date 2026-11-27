# ghsa-manager

A terminal UI for working through a repository's **draft GitHub security
advisories**. For every draft advisory, it shows:

- its temporary private fork, if it has one;
- the open fix PRs and the branches they target;
- each PR's latest CI run in a private mirror (temporary private forks can't
  run GitHub Actions), with a warning when a green run's tested base can't be
  confirmed;
- each PR's reviews, with reviews given on an earlier head marked stale.

Mark advisories for CI and ghsa-manager runs their fix PRs through the mirror's
shared `ghsa-ci.sh`, a few runs at a time. It then posts the results as one
comment on the advisory, edited in place on later runs. For CI started some
other way, `R` posts the advisory's current results without re-running them.

## Setup

ghsa-manager uses your `gh` login (`gh auth login`) and pushes to the mirror
with your SSH key. You need to be a collaborator on the advisories.

Tell it which repositories to use, in `~/.config/ghsa-manager/config.json`:

```json
{"upstream": "owner/repo", "mirror": "owner/repo-private-mirror"}
```

or with `--upstream` and `--mirror`. The mirror must provide `ghsa-ci.sh` with
`--json` support on its `mirror-sync` branch.

Two optional keys:

- `checkout`: the local clone of the security checkout, where a per-advisory
  Claude session runs. A leading `~/` is your home directory. Default:
  `~/sources/<repo name>-security`.
- `supported_branches`: how many of the upstream's newest release branches
  are supported (at least 1). Default: 2.

```json
{"upstream": "owner/repo", "mirror": "owner/repo-private-mirror",
 "checkout": "~/sources/repo-security", "supported_branches": 2}
```

## Use

```
ghsa-manager            # the TUI
ghsa-manager status     # print once and exit
```

Keys: `c` mark for CI · `u` unmark · `t`/`b` send to the top or bottom of the
queue · `x` clear stuck runs · `R` post the current CI results now (no re-run) · `l` open the release checklist · `a` open the advisory's Claude session · `enter` open the advisory · `p` open the PR ·
`f` make a fork · `r` refresh · `q` quit.

On the checklist screen: `space` ticks or unticks the item under the cursor (on
a missing branch, or an item that isn't set, it waives it) · `C` requests a CVE,
after a y/n · `a` opens the advisory's Claude session · `r` refresh · `esc` or `q` goes back.

`a` needs ghsa-manager running inside tmux, and `claude` on your PATH. It opens
a tmux window named after the advisory's GHSA ID, running an interactive Claude
Code session in the security checkout (`checkout` above), with no MCP servers.
Each advisory keeps one session: `a` again switches to its window, or resumes
the session once the window is closed. The session starts knowing the
advisory's ID, links, fork, fix PRs and open checklist items, never its title
or description. It may read the advisory with `gh api`, and change it only
after you agree in the chat; it can request a CVE with
`ghsa-manager request-cve --upstream <upstream> --mirror <mirror> <GHSA>`
(the TUI's own repositories), and must never comment on, publish or change
the state of the advisory.

## Development

Use the nix dev shell (direnv, or `nix develop`), run `lefthook install` once,
then `make check`. The design and plan are in `docs/superpowers/`.
