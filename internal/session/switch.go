package session

import (
	"context"
	"strconv"
	"strings"
)

// Ported from todaydeck's internal/tmuxswitch.
//
// Alan runs every terminal as its own session in one session group, all
// sharing the same windows. A bare `switch-client -t @N` can name another
// session in the group (often its leader), and moving the client there locks
// it in step with every other terminal on that session. So the switch finds
// the client looking at ghsa-manager's own pane and switches that client,
// within its own session.

// clientFormat is what list-clients says about each client, tab-separated.
const clientFormat = "#{client_activity}\t#{client_name}\t#{session_name}\t#{pane_id}"

// switchTo shows window (a window ID, @N) on the client showing o.Pane, in
// that client's own session. When several clients show it, the one used last
// wins: that's where the key was pressed. If no client shows it, it falls back
// to tmux's choice of client and session.
func (o *Opener) switchTo(ctx context.Context, window string) error {
	args := []string{"switch-client", "-t", window}
	if client, sess, ok := o.ownClient(ctx); ok {
		args = []string{"switch-client", "-c", client, "-t", sess + ":" + window}
	}
	_, err := o.Run(ctx, "", "tmux", args...)
	return err
}

// ownClient returns the most recently active client showing o.Pane, and its
// session.
func (o *Opener) ownClient(ctx context.Context) (string, string, bool) {
	if o.Pane == "" {
		return "", "", false
	}
	out, err := o.Run(ctx, "", "tmux", "list-clients", "-F", clientFormat)
	if err != nil {
		return "", "", false
	}
	var client, sess string
	latest := int64(-1)
	for line := range strings.Lines(out) {
		f := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(f) != 4 || f[3] != o.Pane {
			continue
		}
		activity, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		if activity > latest {
			latest, client, sess = activity, f[1], f[2]
		}
	}
	return client, sess, client != ""
}
