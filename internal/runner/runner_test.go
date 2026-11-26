package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeScript(body string) func(context.Context) ([]byte, error) {
	return func(context.Context) ([]byte, error) { return []byte("#!/usr/bin/env bash\n" + body + "\n"), nil }
}

func TestRun_ParsesJSONLines(t *testing.T) {
	r := Runner{Script: fakeScript(`
echo "==> pushing over SSH" >&2
echo '{"ghsa":"GHSA-ABCD-EFGH-IJKL","fork":"o/f","fork_pr":1,"fork_pr_url":"u1","base":"main","head_sha":"h1","base_sha":"b1","mirror_branch":"ghsa-abcd-efgh-ijkl/main-pr1","mirror_pr_url":"m1","action":"reopened","conflicting":false}'
echo '{"ghsa":"GHSA-ABCD-EFGH-IJKL","fork":"o/f","fork_pr":2,"fork_pr_url":"u2","base":"release-4.1","head_sha":"h2","base_sha":"b2","mirror_branch":"ghsa-abcd-efgh-ijkl/release-4.1-pr2","mirror_pr_url":"m2","action":"updated","conflicting":true}'
[ "$1" = --json ] && [ "$2" = GHSA-abcd-efgh-ijkl ] && [ "$3" = 1 ] && [ "$4" = 2 ] || exit 9`)}
	res, err := r.Run(t.Context(), "GHSA-abcd-efgh-ijkl", []int{1, 2})
	require.NoError(t, err)
	require.Len(t, res, 2)
	assert.Equal(t, Result{
		GHSA: "GHSA-abcd-efgh-ijkl", Fork: "o/f", ForkPR: 1, ForkPRURL: "u1", Base: "main", HeadSHA: "h1", BaseSHA: "b1",
		MirrorBranch: "ghsa-abcd-efgh-ijkl/main-pr1", MirrorPRURL: "m1", Action: "reopened",
	}, res[0])
	assert.True(t, res[1].Conflicting)
}

func TestRun_FailureReportsTheScriptsError(t *testing.T) {
	r := Runner{Script: fakeScript(`
echo "==> pushing over SSH" >&2
echo "error: .github/workflows/ci-build.yaml now uses secrets.SONAR_TOKEN. Check what it sends where." >&2
exit 1`)}
	_, err := r.Run(t.Context(), "GHSA-abcd-efgh-ijkl", []int{1})
	require.Error(t, err)
	assert.Equal(t, ".github/workflows/ci-build.yaml now uses secrets.SONAR_TOKEN. Check what it sends where.", err.Error())
}

func TestRun_FailureWithoutAnErrorLineUsesTheLastLine(t *testing.T) {
	r := Runner{Script: fakeScript(`echo "first" >&2; echo "ssh: connect to host github.com: timed out" >&2; exit 255`)}
	_, err := r.Run(t.Context(), "GHSA-abcd-efgh-ijkl", []int{1})
	require.Error(t, err)
	assert.Equal(t, "ssh: connect to host github.com: timed out", err.Error())
}

// Review #3: when the script dies partway, the PRs it already started are
// still returned alongside the error.
func TestRun_PartialFailureKeepsStartedPRs(t *testing.T) {
	r := Runner{Script: fakeScript(`
echo '{"ghsa":"GHSA-abcd-efgh-ijkl","fork_pr":1,"base":"main","head_sha":"h1","base_sha":"b1","mirror_branch":"m1","action":"updated","conflicting":false}'
echo "error: PR head moved while running; try again" >&2
exit 1`)}
	res, err := r.Run(t.Context(), "GHSA-abcd-efgh-ijkl", []int{1, 2})
	require.Error(t, err)
	assert.Equal(t, "PR head moved while running; try again", err.Error())
	require.Len(t, res, 1)
	assert.Equal(t, 1, res[0].ForkPR)
}
