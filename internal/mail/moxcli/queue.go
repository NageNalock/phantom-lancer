package moxcli

import (
	"context"
	"strconv"
	"strings"
)

// QueueSummary returns aggregated counts from `mox queue list` by bucket.
func (r *Runner) QueueSummary(ctx context.Context) (*QueueSummary, error) {
	stdout, stderr, _, _ := r.run(ctx, "queue", []string{"list"})
	out := stdout
	if out == "" {
		out = stderr
	}
	qs := &QueueSummary{}
	// Best-effort: count lines matching each status prefix.
	for _, line := range strings.Split(out, "\n") {
		l := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(l, "hold") {
			qs.Hold++
		} else if strings.Contains(l, "scheduled") {
			qs.Scheduled++
		} else if strings.Contains(l, "failed") {
			qs.Failed++
		} else if strings.Contains(l, "dropped") {
			qs.Dropped++
		} else if strings.Contains(l, "suppressed") {
			qs.Suppressed++
		}
	}
	// Also try numeric parsing: "Status: hold=10 scheduled=5" style
	for _, line := range strings.Split(out, "\n") {
		l := strings.ToLower(strings.TrimSpace(line))
		for _, tok := range strings.Fields(l) {
			if strings.HasPrefix(tok, "hold=") {
				if n, err := strconv.ParseInt(strings.TrimPrefix(tok, "hold="), 10, 64); err == nil {
					qs.Hold = n
				}
			}
			if strings.HasPrefix(tok, "scheduled=") {
				if n, err := strconv.ParseInt(strings.TrimPrefix(tok, "scheduled="), 10, 64); err == nil {
					qs.Scheduled = n
				}
			}
			if strings.HasPrefix(tok, "failed=") {
				if n, err := strconv.ParseInt(strings.TrimPrefix(tok, "failed="), 10, 64); err == nil {
					qs.Failed = n
				}
			}
			if strings.HasPrefix(tok, "dropped=") {
				if n, err := strconv.ParseInt(strings.TrimPrefix(tok, "dropped="), 10, 64); err == nil {
					qs.Dropped = n
				}
			}
			if strings.HasPrefix(tok, "suppressed=") {
				if n, err := strconv.ParseInt(strings.TrimPrefix(tok, "suppressed="), 10, 64); err == nil {
					qs.Suppressed = n
				}
			}
		}
	}
	return qs, nil
}

// QueueAction runs `mox queue <action> <id> [ids...]`.
// action: "hold", "unhold", "schedule", "fail", "drop".
func (r *Runner) QueueAction(ctx context.Context, action string, ids []string) (bool, string, error) {
	args := append([]string{action}, ids...)
	stdout, stderr, exit, err := r.run(ctx, "queue", args)
	out := stdout + "\n" + stderr
	return exit == 0 && err == nil, strings.TrimSpace(out), err
}

// SuppressionAdd runs `mox suppression add <pattern>`.
func (r *Runner) SuppressionAdd(ctx context.Context, pattern string) (bool, string, error) {
	stdout, stderr, exit, err := r.run(ctx, "suppression", []string{"add", pattern})
	out := stdout + "\n" + stderr
	return exit == 0 && err == nil, strings.TrimSpace(out), err
}

// SuppressionRemove runs `mox suppression drop <pattern>`.
func (r *Runner) SuppressionRemove(ctx context.Context, pattern string) (bool, string, error) {
	stdout, stderr, exit, err := r.run(ctx, "suppression", []string{"drop", pattern})
	out := stdout + "\n" + stderr
	return exit == 0 && err == nil, strings.TrimSpace(out), err
}
