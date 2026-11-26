package mirror

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMergedBase(t *testing.T) {
	log := "2026-11-26T12:00:00.0000000Z HEAD is now at f3478d3 Merge b2b514b48d6c954e11e21cacc2356b4710d5414e into a0ff8ba5d56d79772387b10a88416449c056dd65\n"
	head, base, ok := ParseMergedBase(log)
	assert.True(t, ok)
	assert.Equal(t, "b2b514b48d6c954e11e21cacc2356b4710d5414e", head)
	assert.Equal(t, "a0ff8ba5d56d79772387b10a88416449c056dd65", base)
}

func TestParseMergedBase_AbbreviatedBase(t *testing.T) {
	_, base, ok := ParseMergedBase("Merge 35090fbba70cd7a1c84ec8c3d375c4883dbfd71d into b99c4aaaf4\n")
	assert.True(t, ok)
	assert.Equal(t, "b99c4aaaf4", base)
}

func TestParseMergedBase_NoMergeLine(t *testing.T) {
	_, _, ok := ParseMergedBase("checking out the head directly\n")
	assert.False(t, ok)
}

func TestBranchName(t *testing.T) {
	assert.Equal(t, "ghsa-abcd-efgh-ijkl/release-4.1-pr2", BranchName("GHSA-ABCD-efgh-ijkl", "release-4.1", 2))
}
