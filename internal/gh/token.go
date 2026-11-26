// Package gh holds ghsa-manager's GitHub access: the token from the gh CLI's
// stored login, the API client, and the repositories it works with.
package gh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Token returns the gh CLI's stored login token (`gh auth token`). It never
// reads GITHUB_TOKEN or GH_TOKEN: the login is the one credential the tool uses.
func Token(ctx context.Context) (string, error) {
	return tokenFrom(ctx, "gh")
}

func tokenFrom(ctx context.Context, ghBin string) (string, error) {
	cmd := exec.CommandContext(ctx, ghBin, "auth", "token")
	// gh prefers GH_TOKEN/GITHUB_TOKEN over the stored login, so drop them.
	cmd.Env = filteredEnv(cmd.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh auth token: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	tok := strings.TrimSpace(stdout.String())
	if tok == "" {
		return "", errors.New("gh auth token printed nothing; run: gh auth login")
	}
	return tok, nil
}

func filteredEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GH_TOKEN=") || strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
