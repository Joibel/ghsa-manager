package gh

import (
	"fmt"
	"strings"

	"github.com/google/go-github/v90/github"
)

// Repo is a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

// ParseRepo parses "owner/name".
func ParseRepo(s string) (Repo, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repo{}, fmt.Errorf("%q isn't a repository; use owner/name", s)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// String is "owner/name".
func (r Repo) String() string { return r.Owner + "/" + r.Name }

// Repos are the repositories ghsa-manager works with: the upstream whose draft
// advisories it tracks, and the private mirror where their fixes' CI runs.
type Repos struct {
	Upstream Repo
	Mirror   Repo
}

// NewClient returns a go-github client authenticated with token. Tests pass
// extra options, such as github.WithURLs pointing at an httptest server.
func NewClient(token string, opts ...github.ClientOptionsFunc) (*github.Client, error) {
	c, err := github.NewClient(append([]github.ClientOptionsFunc{github.WithAuthToken(token)}, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("creating GitHub client: %w", err)
	}
	return c, nil
}
