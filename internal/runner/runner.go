// Package runner starts mirror CI by running the shared ghsa-ci.sh, fetched
// live from the mirror's mirror-sync branch, in its --json mode.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/go-github/v90/github"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/model"
)

// Result is one fix PR's line of `ghsa-ci.sh --json` output.
type Result struct {
	GHSA         string `json:"ghsa"`
	Fork         string `json:"fork"`
	ForkPRURL    string `json:"fork_pr_url"`
	Base         string `json:"base"`
	HeadSHA      string `json:"head_sha"`
	BaseSHA      string `json:"base_sha"`
	MirrorBranch string `json:"mirror_branch"`
	MirrorPRURL  string `json:"mirror_pr_url"`
	// Action is opened, updated or reopened (CI restarted), or unchanged (it wasn't).
	Action      string `json:"action"`
	ForkPR      int    `json:"fork_pr"`
	Conflicting bool   `json:"conflicting"`
}

// Runner runs ghsa-ci.sh.
type Runner struct {
	// Script returns the script to run. FromMirror fetches the shared copy.
	Script func(context.Context) ([]byte, error)
}

// FromMirror returns a Script func that fetches ghsa-ci.sh from the mirror's
// mirror-sync branch on every call, so the tool always runs the shared copy.
func FromMirror(c *github.Client, mirror gh.Repo) func(context.Context) ([]byte, error) {
	return func(ctx context.Context) ([]byte, error) {
		rc, _, err := c.Repositories.DownloadContents(ctx, mirror.Owner, mirror.Name, "ghsa-ci.sh",
			&github.RepositoryContentGetOptions{Ref: "mirror-sync"})
		if err != nil {
			return nil, fmt.Errorf("fetching ghsa-ci.sh from %s mirror-sync: %w", mirror, err)
		}
		b, err := io.ReadAll(rc)
		if cerr := rc.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, fmt.Errorf("reading ghsa-ci.sh: %w", err)
		}
		return b, nil
	}
}

// Run runs ghsa-ci.sh for the given fix PRs of an advisory. It blocks while the
// script pushes, which needs a YubiKey touch. On failure the error is the
// script's own error line, and the results are the PRs it started before
// failing.
func (r Runner) Run(ctx context.Context, ghsa string, prs []int) ([]Result, error) {
	script, err := r.Script(ctx)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "ghsa-manager-run-")
	if err != nil {
		return nil, fmt.Errorf("creating a temp dir for ghsa-ci.sh: %w", err)
	}
	res, runErr := run(ctx, dir, script, ghsa, prs)
	if err := os.RemoveAll(dir); err != nil && runErr == nil {
		runErr = fmt.Errorf("removing %s: %w", dir, err)
	}
	return res, runErr
}

func run(ctx context.Context, dir string, script []byte, ghsa string, prs []int) ([]Result, error) {
	path := filepath.Join(dir, "ghsa-ci.sh")
	if err := os.WriteFile(path, script, 0o600); err != nil {
		return nil, fmt.Errorf("writing ghsa-ci.sh: %w", err)
	}
	args := []string{path, "--json", ghsa}
	for _, n := range prs {
		args = append(args, strconv.Itoa(n))
	}
	cmd := exec.CommandContext(ctx, "bash", args...) //nolint:gosec // runs the shared ghsa-ci.sh; arguments are an advisory ID and PR numbers
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	out, parseErr := parse(&stdout)
	if runErr != nil {
		// PRs the script reported before failing did start.
		return out, scriptError(stderr.String(), runErr)
	}
	return out, parseErr
}

func parse(stdout *bytes.Buffer) ([]Result, error) {
	var out []Result
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var res Result
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			return out, fmt.Errorf("parsing ghsa-ci.sh output %q: %w", line, err)
		}
		res.GHSA = model.NormalizeID(res.GHSA)
		out = append(out, res)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("reading ghsa-ci.sh output: %w", err)
	}
	return out, nil
}

// scriptError picks the script's `error: ...` line (from its die()), or failing
// that its last stderr line, as the error to show.
func scriptError(stderr string, runErr error) error {
	var last string
	for line := range strings.Lines(stderr) {
		line = strings.TrimSpace(line)
		if msg, ok := strings.CutPrefix(line, "error: "); ok {
			return errors.New(msg)
		}
		if line != "" {
			last = line
		}
	}
	if last != "" {
		return errors.New(last)
	}
	return fmt.Errorf("ghsa-ci.sh failed: %w", runErr)
}
