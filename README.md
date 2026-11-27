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
queue · `x` clear stuck runs · `R` post the current CI results now (no re-run) · `enter` open the advisory · `p` open the PR ·
`f` make a fork · `r` refresh · `q` quit.

## Development

Use the nix dev shell (direnv, or `nix develop`), run `lefthook install` once,
then `make check`. The design and plan are in `docs/superpowers/`.
