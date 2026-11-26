package mirror

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/google/go-github/v90/github"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/model"
)

const (
	ciWorkflow = "ci-build.yaml"
	// checkoutJob is a short job whose log records the merge commit the run tested.
	checkoutJob = "Get changed files"
	maxLogBytes = 4 << 20
)

// testJob matches the test jobs whose skipping a green run can hide; the same
// pattern as the mirror's ghsa-ci-report.yaml.
var testJob = regexp.MustCompile(`^((Windows )?Unit Tests|E2E Tests( \(|$)|E2E UI)`)

// Facts reads mirror CI runs. It caches job logs, which never change once a
// job completes.
type Facts struct {
	c *github.Client
	// logHTTP downloads job logs from the pre-signed URL GitHub redirects to. It
	// is a plain client, so the GitHub token is never sent to that host.
	logHTTP   *http.Client
	logCache  map[int64]string
	jobsCache map[int64][]*github.WorkflowJob
	// repo is the private mirror.
	repo gh.Repo
	mu   sync.Mutex
}

// NewFacts returns a Facts reading the mirror repo, using c for the API and
// logHTTP (nil for a default client) for log downloads.
func NewFacts(c *github.Client, logHTTP *http.Client, repo gh.Repo) *Facts {
	if logHTTP == nil {
		logHTTP = &http.Client{Timeout: time.Minute}
	}
	return &Facts{c: c, repo: repo, logHTTP: logHTTP, logCache: map[int64]string{}, jobsCache: map[int64][]*github.WorkflowJob{}}
}

// LatestCI returns the latest pull_request CI run on a mirror branch. recorded is
// the base the tool triggered its own run against, if any; currentBase is the
// mirror base branch's tip now.
func (f *Facts) LatestCI(ctx context.Context, branch string, recorded model.RecordedBase, currentBase string) (model.CI, error) {
	ci := model.CI{CurrentBaseSHA: currentBase}
	runs, _, err := f.c.Actions.ListWorkflowRunsByFileName(ctx, f.repo.Owner, f.repo.Name, ciWorkflow,
		&github.ListWorkflowRunsOptions{Branch: branch, Event: "pull_request", ListOptions: github.ListOptions{PerPage: 1}})
	if err != nil {
		return ci, fmt.Errorf("listing CI runs on %s: %w", branch, err)
	}
	if len(runs.WorkflowRuns) == 0 {
		return ci, nil
	}
	run := runs.WorkflowRuns[0]
	ci.RunID = run.GetID()
	ci.StartedAt = run.GetRunStartedAt().Time
	ci.RunURL = run.GetHTMLURL()
	ci.RunHeadSHA = run.GetHeadSHA()
	completed := run.GetStatus() == "completed"
	if completed {
		ci.FinishedAt = run.GetUpdatedAt().Time
	}

	jobs, err := f.jobs(ctx, run.GetID(), completed)
	if err != nil {
		return ci, err
	}
	var checkoutJobID int64
	for _, j := range jobs {
		if j.GetConclusion() == "skipped" && testJob.MatchString(j.GetName()) {
			ci.SkippedTestJobs = append(ci.SkippedTestJobs, j.GetName())
		}
		if j.GetName() == checkoutJob {
			checkoutJobID = j.GetID()
		}
	}

	conclusion := ""
	if completed {
		conclusion = run.GetConclusion()
		ci.MergedBaseSHA = f.mergedBase(ctx, checkoutJobID)
	}
	ci.State = model.ClassifyRun(conclusion, recorded.For(ci.RunID, ci.StartedAt), ci.MergedBaseSHA)
	return ci, nil
}

// jobs lists a run's latest jobs. A completed run's jobs never change, so they
// are cached.
func (f *Facts) jobs(ctx context.Context, runID int64, completed bool) ([]*github.WorkflowJob, error) {
	f.mu.Lock()
	cached, ok := f.jobsCache[runID]
	f.mu.Unlock()
	if ok {
		return cached, nil
	}
	jobs, _, err := f.c.Actions.ListWorkflowJobs(ctx, f.repo.Owner, f.repo.Name, runID,
		&github.ListWorkflowJobsOptions{Filter: "latest", ListOptions: github.ListOptions{PerPage: 100}})
	if err != nil {
		return nil, fmt.Errorf("listing jobs of run %d: %w", runID, err)
	}
	if completed {
		f.mu.Lock()
		f.jobsCache[runID] = jobs.Jobs
		f.mu.Unlock()
	}
	return jobs.Jobs, nil
}

// mergedBase reads the base a run's merge commit was built on from its checkout
// job's log. It is "" when there's no such job or line, or the log can't be read
// (logs expire): the run then shows unconfirmed rather than failing the fetch.
func (f *Facts) mergedBase(ctx context.Context, checkoutJobID int64) string {
	if checkoutJobID == 0 {
		return ""
	}
	log, err := f.jobLog(ctx, checkoutJobID)
	if err != nil {
		return ""
	}
	_, base, _ := ParseMergedBase(log)
	return base
}

func (f *Facts) jobLog(ctx context.Context, jobID int64) (string, error) {
	f.mu.Lock()
	log, ok := f.logCache[jobID]
	f.mu.Unlock()
	if ok {
		return log, nil
	}
	u, _, err := f.c.Actions.GetWorkflowJobLogs(ctx, f.repo.Owner, f.repo.Name, jobID, 1)
	if err != nil {
		return "", fmt.Errorf("finding the log of job %d: %w", jobID, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("building the log request for job %d: %w", jobID, err)
	}
	resp, err := f.logHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading the log of job %d: %w", jobID, err)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxLogBytes))
	if cerr := resp.Body.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("reading the log of job %d: %w", jobID, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading the log of job %d: %s", jobID, resp.Status)
	}
	f.mu.Lock()
	f.logCache[jobID] = string(b)
	f.mu.Unlock()
	return string(b), nil
}

// InFlight counts the mirror's queued and in-progress CI runs, whoever started them.
func (f *Facts) InFlight(ctx context.Context) (int, error) {
	total := 0
	for _, status := range []string{"queued", "in_progress"} {
		runs, _, err := f.c.Actions.ListWorkflowRunsByFileName(ctx, f.repo.Owner, f.repo.Name, ciWorkflow,
			&github.ListWorkflowRunsOptions{Status: status, ListOptions: github.ListOptions{PerPage: 1}})
		if err != nil {
			return 0, fmt.Errorf("counting %s CI runs: %w", status, err)
		}
		total += runs.GetTotalCount()
	}
	return total, nil
}
