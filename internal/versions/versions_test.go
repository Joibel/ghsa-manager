package versions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Parallel()

	v, err := Parse("v4.1.0-rc1")
	require.NoError(t, err)
	assert.Equal(t, Version{Major: 4, Minor: 1, Patch: 0, Pre: "rc1"}, v)

	// Advisories' ranges sometimes give two-part versions: X.Y is X.Y.0.
	v, err = Parse("3.5")
	require.NoError(t, err)
	assert.Equal(t, Version{Major: 3, Minor: 5, Patch: 0}, v)
	v, err = Parse("v3.5-rc1")
	require.NoError(t, err)
	assert.Equal(t, Version{Major: 3, Minor: 5, Patch: 0, Pre: "rc1"}, v)

	for _, bad := range []string{"", "4", "v4", "4.", "4.1.", "4.x", "4.1.x", "4.1.0-", "a.b.c", "+4.1.0", "4.1.0.1"} {
		_, err := Parse(bad)
		require.Error(t, err, bad)
	}
}

func TestLess(t *testing.T) {
	t.Parallel()

	mustParse := func(s string) Version {
		v, err := Parse(s)
		require.NoError(t, err)
		return v
	}
	assert.True(t, mustParse("4.0.9").Less(mustParse("4.1.0")))
	assert.True(t, mustParse("4.1.0-rc1").Less(mustParse("4.1.0")))
	assert.True(t, mustParse("4.1.0-rc1").Less(mustParse("4.1.0-rc2")))
	assert.False(t, mustParse("4.1.0").Less(mustParse("4.1.0-rc1")))
	assert.False(t, mustParse("4.1.0").Less(mustParse("4.1.0")))
}

func TestParseRange(t *testing.T) {
	t.Parallel()

	good := []string{
		">= 4.0.0, < 4.0.7",
		"<4.0.7",
		"<= 3.6.10",
		"= 4.1.0",
		"4.1.0",
		">= 4.1.0-rc1, < 4.1.3",
		">= v4.0.0,<v4.0.7",
		"  >=   4.0.0 ,   <  4.0.7  ",
	}
	for _, s := range good {
		_, err := ParseRange(s)
		require.NoError(t, err, s)
	}

	for _, s := range []string{"", ">= ", "~> 4.0", ">= 4.0.0,", "bad"} {
		_, err := ParseRange(s)
		require.Error(t, err, s)
	}
}

func TestCoversMinor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		rng    string
		covers []string
		not    []string
	}{
		{">= 4.0.0, < 4.0.7", []string{"4.0"}, []string{"4.1", "3.7"}},
		{"< 3.7.0", []string{"3.6"}, []string{"3.7"}},
		{"<= 3.6.10", []string{"3.6"}, []string{"3.7"}},
		{"= 4.1.0", []string{"4.1"}, []string{"4.0", "4.2"}},
		{">= 4.1.0-rc1, < 4.1.3", []string{"4.1"}, []string{"4.0", "4.2"}},
		{"< 4.2.0", []string{"4.1"}, []string{"4.2"}},
		{"< 4.2.0-rc2", []string{"4.2"}, nil},
		{">= 4.0.0, < 3.0.0", nil, []string{"3.0", "4.0"}},
		{"< 3.5", []string{"3.4"}, []string{"3.5"}},
		{">= 4.1, < 4.1.3", []string{"4.1"}, []string{"4.0", "4.2"}},
	}
	for _, tt := range tests {
		r, err := ParseRange(tt.rng)
		require.NoError(t, err, tt.rng)
		for _, m := range tt.covers {
			assert.True(t, r.CoversMinor(minor(t, m)), "%s covers %s", tt.rng, m)
		}
		for _, m := range tt.not {
			assert.False(t, r.CoversMinor(minor(t, m)), "%s must not cover %s", tt.rng, m)
		}
	}
}

func minor(t *testing.T, s string) (int, int) {
	t.Helper()

	v, err := Parse(s + ".0")
	require.NoError(t, err)

	return v.Major, v.Minor
}

func TestAnyCoversMinor(t *testing.T) {
	t.Parallel()

	ranges := []string{">= 4.0.0, < 4.0.7", "< 3.7.5"}
	for _, m := range []string{"4.0", "3.7"} {
		maj, mnr := minor(t, m)
		got, err := AnyCoversMinor(ranges, maj, mnr)
		require.NoError(t, err)
		assert.True(t, got, m)
	}
	got, err := AnyCoversMinor(ranges, 4, 1)
	require.NoError(t, err)
	assert.False(t, got)

	_, err = AnyCoversMinor([]string{"bad"}, 4, 0)
	require.Error(t, err)
}
