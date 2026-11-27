// Package config reads which repositories ghsa-manager works with, from
// $XDG_CONFIG_HOME/ghsa-manager/config.json and flags:
//
//	{"upstream": "owner/repo", "mirror": "owner/private-mirror"}
//
// The upstream and its private CI mirror are always configured, never
// compiled in. Two optional keys have defaults derived from them:
// "checkout" (the security checkout, default $HOME/sources/<name>-security)
// and "supported_branches" (default 2).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Joibel/ghsa-manager/internal/gh"
)

type file struct {
	SupportedBranches *int   `json:"supported_branches"`
	Upstream          string `json:"upstream"`
	Mirror            string `json:"mirror"`
	Checkout          string `json:"checkout"`
}

// defaultSupportedBranches is how many release branches are supported when
// the config does not say.
const defaultSupportedBranches = 2

// Config is everything read from the config file and flags.
type Config struct {
	Repos             gh.Repos
	Checkout          string // absolute; default $HOME/sources/<upstream name>-security
	SupportedBranches int    // default 2
}

// expandHome expands a leading "~/" (or a bare "~") to home; any other path
// is returned as given.
func expandHome(p, home string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}

// Path is the config file: $XDG_CONFIG_HOME/ghsa-manager/config.json, or
// ~/.config/ghsa-manager/config.json.
func Path() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "ghsa-manager", "config.json")
}

// Load reads the config file at path (it may be missing) and applies the
// upstream and mirror flags over it ("" leaves the file's value). A
// supported_branches below 1 is an error.
func Load(path, upstream, mirror string) (Config, error) {
	var f file
	b, err := os.ReadFile(path) //nolint:gosec // the tool's own config file
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	default:
		if jerr := json.Unmarshal(b, &f); jerr != nil {
			return Config{}, fmt.Errorf("parsing %s: %w", path, jerr)
		}
	}
	if upstream != "" {
		f.Upstream = upstream
	}
	if mirror != "" {
		f.Mirror = mirror
	}
	if f.Upstream == "" || f.Mirror == "" {
		return Config{}, fmt.Errorf("set the upstream and mirror repositories in %s or with --upstream and --mirror", path)
	}
	up, err := gh.ParseRepo(f.Upstream)
	if err != nil {
		return Config{}, fmt.Errorf("upstream: %w", err)
	}
	mi, err := gh.ParseRepo(f.Mirror)
	if err != nil {
		return Config{}, fmt.Errorf("mirror: %w", err)
	}
	branches := defaultSupportedBranches
	if f.SupportedBranches != nil {
		branches = *f.SupportedBranches
	}
	if branches < 1 {
		return Config{}, fmt.Errorf("supported_branches must be at least 1, got %d", branches)
	}
	home := os.Getenv("HOME")
	checkout := expandHome(f.Checkout, home)
	if checkout == "" {
		checkout = filepath.Join(home, "sources", up.Name+"-security")
	}
	return Config{Repos: gh.Repos{Upstream: up, Mirror: mi}, Checkout: checkout, SupportedBranches: branches}, nil
}
