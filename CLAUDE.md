# CLAUDE.md

ghsa-manager is a local TUI for a repository's draft security advisories. The design is in `docs/superpowers/specs/`.

## Build and test

Work inside the nix dev shell (direnv, or `nix develop`), which pins Go and
every tool. Run `lefthook install` once per clone.

- `make check`: the Go version-pin check, lint, tests (`-race`), gitleaks and govulncheck
- `make build`: builds `bin/ghsa-manager`

## Security rules

Everything this tool touches is embargoed security work.

- Only talk to `api.github.com`, `github.com`, and git over SSH to the private mirror
  (configured as `mirror`). No other services, no telemetry. The one exception is the
  interactive Claude Code session that `a` opens, which Alan has chosen to allow; it runs
  with no MCP servers.
- Read the token from `gh auth token` only. Never read `GITHUB_TOKEN`/`GH_TOKEN` or add a token flag.
- Never log advisory titles or details anywhere except the screen.
- Local state lives in `$XDG_STATE_HOME/ghsa-manager/` (dir `0700`, files `0600`).
- CI runs through the shared `ghsa-ci.sh` from the mirror's `mirror-sync` branch,
  fetched live. Don't vendor or pin it; improve the shared script instead.
