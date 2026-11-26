// Package config reads which repositories ghsa-manager works with, from
// $XDG_CONFIG_HOME/ghsa-manager/config.json and flags:
//
//	{"upstream": "owner/repo", "mirror": "owner/private-mirror"}
//
// There are no defaults: the upstream and its private CI mirror are always
// configured, never compiled in.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Joibel/ghsa-manager/internal/gh"
)

type file struct {
	Upstream string `json:"upstream"`
	Mirror   string `json:"mirror"`
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
// upstream and mirror flags over it ("" leaves the file's value).
func Load(path, upstream, mirror string) (gh.Repos, error) {
	var f file
	b, err := os.ReadFile(path) //nolint:gosec // the tool's own config file
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return gh.Repos{}, fmt.Errorf("reading %s: %w", path, err)
	default:
		if jerr := json.Unmarshal(b, &f); jerr != nil {
			return gh.Repos{}, fmt.Errorf("parsing %s: %w", path, jerr)
		}
	}
	if upstream != "" {
		f.Upstream = upstream
	}
	if mirror != "" {
		f.Mirror = mirror
	}
	if f.Upstream == "" || f.Mirror == "" {
		return gh.Repos{}, fmt.Errorf("set the upstream and mirror repositories in %s or with --upstream and --mirror", path)
	}
	up, err := gh.ParseRepo(f.Upstream)
	if err != nil {
		return gh.Repos{}, fmt.Errorf("upstream: %w", err)
	}
	mi, err := gh.ParseRepo(f.Mirror)
	if err != nil {
		return gh.Repos{}, fmt.Errorf("mirror: %w", err)
	}
	return gh.Repos{Upstream: up, Mirror: mi}, nil
}
