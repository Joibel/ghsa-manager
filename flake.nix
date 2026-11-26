{
  description = "ghsa-manager dev environment";

  # Adapted from todaydeck's flake (itself modelled on steadystate-mono's):
  # stable nixpkgs for the basics, unstable for the toolchain and linters.
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
    nixpkgs-unstable.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    {
      nixpkgs,
      nixpkgs-unstable,
      ...
    }:
    let
      supportedSystems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          unstable = nixpkgs-unstable.legacyPackages.${system};

          # Go, pinned ahead of nixpkgs (steadystate-mono's approach), so a
          # stdlib security release reaches us without waiting for nixpkgs.
          #
          # This is the ONLY place the version is written. Renovate bumps
          # the line below and runs scripts/go-toolchain-sync.sh, which
          # derives the src hash and go.mod's `go` directive from it. Don't
          # hand-edit the hash: run `make go-toolchain-sync`.
          goVersion = "1.27.4"; # renovate: datasource=golang-version depName=go
          # Override the nixpkgs Go matching our major.minor, not the default
          # `go`: nixpkgs' per-release patches only apply to their own source.
          goBase = unstable."go_${builtins.replaceStrings [ "." ] [ "_" ] (unstable.lib.versions.majorMinor goVersion)}";
          go = goBase.overrideAttrs (
            final: prev: {
              version = goVersion;
              src = unstable.fetchurl {
                url = "https://go.dev/dl/go${goVersion}.src.tar.gz";
                # go-toolchain-sync: hash (derived from goVersion; do not hand-edit)
                hash = "sha256-c8+4VxrsDAEXdofbhxY9Rqog6XeWcncokaoXZU2aYJk=";
              };
            }
          );
        in
        {
          default = pkgs.mkShell {
            buildInputs = [
              # go.mod's `go` directive must equal this (`make go-pin-check`).
              go
              pkgs.gnumake

              # Checks (`make check`).
              unstable.golangci-lint
              unstable.govulncheck
              unstable.gitleaks
              # ghsa-ci.sh, which ghsa-manager runs, is linted with shellcheck
              # and parses its --json output with jq.
              unstable.shellcheck
              pkgs.jq

              # Git hooks (lefthook.yml); node backs the commitlint
              # commit-msg hook via npx.
              unstable.lefthook
              unstable.nodejs_22
            ];

            # Use the toolchain above; never download another one.
            GOTOOLCHAIN = "local";
          };
        }
      );

      formatter = forAllSystems (system: nixpkgs.legacyPackages.${system}.nixfmt-rfc-style);
    };
}
