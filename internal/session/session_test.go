package session_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/session"
)

const ghsa = "GHSA-aaaa-bbbb-cccc"

var (
	upstream = gh.Repo{Owner: "argoproj", Name: "argo-workflows"}               //nolint:gochecknoglobals // test fixture
	mirror   = gh.Repo{Owner: "pipekit", Name: "argo-workflows-private-mirror"} //nolint:gochecknoglobals // test fixture
)

// fake records commands and answers tmux list-windows, list-clients and
// new-window.
type fake struct {
	windows     string
	clients     string
	calls       []string
	clientsFail bool
}

func (f *fake) run(_ context.Context, dir, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, dir+"$ "+line)
	switch {
	case strings.HasPrefix(line, "tmux list-windows"):
		return f.windows, nil
	case strings.HasPrefix(line, "tmux list-clients"):
		if f.clientsFail {
			return "", errors.New("boom")
		}
		return f.clients, nil
	case strings.HasPrefix(line, "tmux new-window"):
		return "@7\n", nil
	}
	return "", nil
}

// newWindow is the tmux new-window call.
func (f *fake) newWindow(t *testing.T) string {
	t.Helper()
	for _, c := range f.calls {
		if strings.Contains(c, "tmux new-window") {
			return c
		}
	}
	t.Fatalf("no window opened: %q", f.calls)
	return ""
}

func setup(t *testing.T) (*fake, *session.Opener) {
	t.Helper()
	root := t.TempDir()
	checkout := filepath.Join(root, "argo-workflows-security")
	require.NoError(t, os.MkdirAll(checkout, 0o750))
	f := &fake{}
	return f, &session.Opener{
		Run: f.run, Upstream: upstream, Mirror: mirror, Checkout: checkout,
		ClaudeHome: filepath.Join(root, "claude"), Self: "/usr/local/bin/ghsa-manager", InTmux: true,
	}
}

func advisory() model.Advisory {
	return model.Advisory{
		ID: ghsa, URL: "https://github.com/argoproj/argo-workflows/security/advisories/" + ghsa,
		Summary: "SECRET-TITLE", Description: "SECRET-BODY",
		Fork: "argoproj/argo-workflows-ghsa-aaaa-bbbb-cccc",
		PRs: []model.FixPR{
			{Number: 1, Base: "main", URL: "https://github.com/argoproj/argo-workflows-ghsa-aaaa-bbbb-cccc/pull/1", Title: "SECRET-PR"},
			{Number: 2, Base: "release-4.1", URL: "https://github.com/argoproj/argo-workflows-ghsa-aaaa-bbbb-cccc/pull/2"},
		},
	}
}

var open = []model.Item{model.ItemCVSS, model.ItemCredits} //nolint:gochecknoglobals // test fixture

func TestOpenStarts(t *testing.T) {
	f, o := setup(t)
	msg, err := o.Open(context.Background(), advisory(), open)
	require.NoError(t, err)
	assert.Equal(t, "Started Claude on "+ghsa, msg)
	require.Len(t, f.calls, 3)
	assert.Equal(t, "$ tmux list-windows -a -F #{window_id} #{@ghsa_key}", f.calls[0])
	w := f.calls[1]
	assert.True(t, strings.HasPrefix(w, "$ tmux new-window -P -F #{window_id} -n "+ghsa+" -c "+o.Checkout+" 'claude' "), w)
	assert.Contains(t, w, `'--strict-mcp-config'`)
	assert.Contains(t, w, `'--mcp-config' '{"mcpServers":{}}'`)
	id := session.SessionID(upstream, ghsa)
	// --mcp-config takes several values: "--" ends them, so the prompt isn't
	// read as one.
	assert.Contains(t, w, `'--session-id' '`+id+`' '--' 'This session`)
	assert.Equal(t, "$ tmux set-option -w -t @7 @ghsa_key "+ghsa, f.calls[2])
}

func TestOpenResumes(t *testing.T) {
	f, o := setup(t)
	id := session.SessionID(upstream, ghsa)
	proj := filepath.Join(o.ClaudeHome, "projects", "-x")
	require.NoError(t, os.MkdirAll(proj, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(proj, id+".jsonl"), nil, 0o600))

	msg, err := o.Open(context.Background(), advisory(), open)
	require.NoError(t, err)
	assert.Equal(t, "Resumed Claude on "+ghsa, msg)
	w := f.newWindow(t)
	assert.True(t, strings.HasSuffix(w, `'claude' '--strict-mcp-config' '--mcp-config' '{"mcpServers":{}}' '--resume' '`+id+`'`), w)
	assert.NotContains(t, w, "--session-id")
}

// TestOpenSwitches: Alan's terminals are grouped sessions sharing windows, so
// the switch goes to the client showing ghsa-manager's pane, used last, in its
// own session, not whichever session tmux would pick.
func TestOpenSwitches(t *testing.T) {
	f, o := setup(t)
	o.Pane = "%3"
	f.windows = "@3 \n@4 " + ghsa + "\n"
	f.clients = "100\t/dev/pts/1\tdefault_a\t%9\n" +
		"300\t/dev/pts/2\tdefault_b\t%3\n" + // ghsa-manager's pane, used last
		"200\t/dev/pts/3\tdefault_c\t%3\n" +
		"900\t/dev/pts/4\tdefault_d\t%1\n"
	msg, err := o.Open(context.Background(), advisory(), open)
	require.NoError(t, err)
	assert.Equal(t, "Switched to "+ghsa, msg)
	assert.Equal(t, []string{
		"$ tmux list-windows -a -F #{window_id} #{@ghsa_key}",
		"$ tmux list-clients -F #{client_activity}\t#{client_name}\t#{session_name}\t#{pane_id}",
		"$ tmux switch-client -c /dev/pts/2 -t default_b:@4",
	}, f.calls)
}

// TestOpenSwitchFallsBack: with no client showing ghsa-manager's pane, the
// switch is left to tmux.
func TestOpenSwitchFallsBack(t *testing.T) {
	for name, tc := range map[string]struct {
		clients string
		fail    bool
	}{
		"no client on the pane": {clients: "100\t/dev/pts/1\tdefault_a\t%9\n"},
		"list-clients fails":    {fail: true},
		"garbled":               {clients: "x\t/dev/pts/1\tdefault_a\t%3\nshort\n"},
	} {
		t.Run(name, func(t *testing.T) {
			f, o := setup(t)
			o.Pane = "%3"
			f.windows, f.clients, f.clientsFail = "@4 "+ghsa+"\n", tc.clients, tc.fail
			msg, err := o.Open(context.Background(), advisory(), open)
			require.NoError(t, err)
			assert.Equal(t, "Switched to "+ghsa, msg)
			assert.Equal(t, "$ tmux switch-client -t @4", f.calls[len(f.calls)-1])
		})
	}
}

// TestOpenSwitchOutsideAPane: with no $TMUX_PANE, there's no client to look
// for.
func TestOpenSwitchOutsideAPane(t *testing.T) {
	f, o := setup(t)
	f.windows = "@4 " + ghsa + "\n"
	_, err := o.Open(context.Background(), advisory(), open)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"$ tmux list-windows -a -F #{window_id} #{@ghsa_key}",
		"$ tmux switch-client -t @4",
	}, f.calls)
}

func TestOpenNoTmux(t *testing.T) {
	f, o := setup(t)
	o.InTmux = false
	_, err := o.Open(context.Background(), advisory(), open)
	require.ErrorIs(t, err, session.ErrNoTmux)
	assert.Empty(t, f.calls)
}

func TestOpenNoCheckout(t *testing.T) {
	f, o := setup(t)
	o.Checkout = filepath.Join(t.TempDir(), "missing")
	_, err := o.Open(context.Background(), advisory(), open)
	require.Error(t, err)
	assert.Contains(t, err.Error(), o.Checkout+` is missing; set "checkout" in the config`)
	for _, c := range f.calls {
		assert.NotContains(t, c, "new-window")
	}
	assert.NoDirExists(t, o.Checkout)
}

// A relative checkout would resolve against wherever ghsa-manager started.
func TestOpenRelativeCheckout(t *testing.T) {
	f, o := setup(t)
	o.Checkout = "argo-workflows-security"
	_, err := o.Open(context.Background(), advisory(), open)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
	for _, c := range f.calls {
		assert.NotContains(t, c, "new-window")
	}
}

func TestPromptKeepsDetailsOut(t *testing.T) {
	f, o := setup(t)
	a := advisory()
	p := session.Prompt(o, a, open)
	_, err := o.Open(context.Background(), a, open)
	require.NoError(t, err)
	for name, s := range map[string]string{"prompt": p, "command": f.newWindow(t)} {
		for _, secret := range []string{"SECRET-TITLE", "SECRET-BODY", "SECRET-PR"} {
			assert.NotContains(t, s, secret, name)
		}
		for _, want := range []string{ghsa, a.Fork, "request-cve"} {
			assert.Contains(t, s, want, name)
		}
	}
}

func TestPrompt(t *testing.T) {
	_, o := setup(t)
	a := advisory()
	p := session.Prompt(o, a, open)
	for _, want := range []string{
		ghsa, upstream.String(), a.URL, a.Fork,
		a.PRs[0].URL + " (base main)", a.PRs[1].URL + " (base release-4.1)",
		"cvss, credits",
		"gh api repos/argoproj/argo-workflows/security-advisories/" + ghsa,
		"gh api -X PATCH repos/argoproj/argo-workflows/security-advisories/" + ghsa,
		"only after Alan agrees in this chat",
		"'/usr/local/bin/ghsa-manager' request-cve --upstream argoproj/argo-workflows --mirror pipekit/argo-workflows-private-mirror " + ghsa,
		"Never post comments on the advisory",
		"never push to the public upstream",
	} {
		assert.Contains(t, p, want)
	}
	assert.True(t, strings.HasSuffix(p, "Then ask Alan what to work on."), p)
}

func TestSessionIDStable(t *testing.T) {
	id := session.SessionID(upstream, ghsa)
	assert.Equal(t, id, session.SessionID(upstream, ghsa))
	assert.NotEqual(t, id, session.SessionID(upstream, "GHSA-dddd-eeee-ffff"))
	_, err := uuid.Parse(id)
	require.NoError(t, err)
}
