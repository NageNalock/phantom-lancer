package moxcli

import (
	"bufio"
	"context"
	"strings"
)

// AliasAdd runs `mox alias add <alias> mode=... recipients=...`.
func (r *Runner) AliasAdd(ctx context.Context, a AliasEntry) (bool, string, error) {
	args := []string{"add", a.AliasAddr}
	if a.Mode != "" {
		args = append(args, "mode="+a.Mode)
	}
	if len(a.Recipients) > 0 {
		args = append(args, "recipients="+strings.Join(a.Recipients, ","))
	}
	stdout, stderr, code, err := r.run(ctx, "alias", args)
	out := stdout + "\n" + stderr
	return code == 0 && err == nil, out, err
}

// AliasDelete runs `mox alias remove <alias>`.
func (r *Runner) AliasDelete(ctx context.Context, aliasAddr string) (bool, string, error) {
	stdout, stderr, code, err := r.run(ctx, "alias", []string{"remove", aliasAddr})
	out := stdout + "\n" + stderr
	return code == 0 && err == nil, out, err
}

// AliasList runs `mox alias list` and parses best-effort.
func (r *Runner) AliasList(ctx context.Context) ([]AliasEntry, error) {
	stdout, _, _, err := r.run(ctx, "alias", []string{"list"})
	if err != nil {
		return nil, err
	}
	var entries []AliasEntry
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		recipients := []string{}
		if len(fields) > 1 {
			recipients = strings.Split(fields[len(fields)-1], ",")
		}
		entries = append(entries, AliasEntry{
			AliasAddr:  fields[0],
			Recipients: recipients,
		})
	}
	return entries, nil
}
