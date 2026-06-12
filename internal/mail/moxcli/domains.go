package moxcli

import (
	"bufio"
	"context"
	"strings"
)

// DomainAdd runs `mox domain add <name>` with optional key=value flags.
func (r *Runner) DomainAdd(ctx context.Context, d DomainEntry) (bool, string, error) {
	args := []string{"add", d.Domain}
	if d.DKIMSelector != "" {
		args = append(args, "dkimselector="+d.DKIMSelector)
	}
	if d.DMARCPolicy != "" {
		args = append(args, "dmarcpolicy="+d.DMARCPolicy)
	}
	if d.DMARCRUA != "" {
		args = append(args, "dmarc.rua="+d.DMARCRUA)
	}
	if d.SPFInclude != "" {
		args = append(args, "spfinclude="+d.SPFInclude)
	}
	if d.WebmailLoginURL != "" {
		args = append(args, "webmailloginurl="+d.WebmailLoginURL)
	}
	stdout, stderr, code, err := r.run(ctx, "domain", args)
	out := stdout + "\n" + stderr
	return code == 0 && err == nil, out, err
}

// DomainUpdate runs `mox domain set <name> key=value …`.
func (r *Runner) DomainUpdate(ctx context.Context, d DomainEntry) (bool, string, error) {
	args := []string{"set", d.Domain}
	if d.DKIMSelector != "" {
		args = append(args, "dkimselector="+d.DKIMSelector)
	}
	if d.DMARCPolicy != "" {
		args = append(args, "dmarcpolicy="+d.DMARCPolicy)
	}
	if d.DMARCRUA != "" {
		args = append(args, "dmarc.rua="+d.DMARCRUA)
	}
	if d.SPFInclude != "" {
		args = append(args, "spfinclude="+d.SPFInclude)
	}
	if d.WebmailLoginURL != "" {
		args = append(args, "webmailloginurl="+d.WebmailLoginURL)
	}
	stdout, stderr, code, err := r.run(ctx, "domain", args)
	out := stdout + "\n" + stderr
	return code == 0 && err == nil, out, err
}

// DomainDelete runs `mox domain remove <name>`.
func (r *Runner) DomainDelete(ctx context.Context, domain string) (bool, string, error) {
	stdout, stderr, code, err := r.run(ctx, "domain", []string{"remove", domain})
	out := stdout + "\n" + stderr
	return code == 0 && err == nil, out, err
}

// DomainList runs `mox domain list` and parses best-effort lines.
func (r *Runner) DomainList(ctx context.Context) ([]DomainEntry, error) {
	stdout, _, _, err := r.run(ctx, "domain", []string{"list"})
	if err != nil {
		return nil, err
	}
	var entries []DomainEntry
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
		entries = append(entries, DomainEntry{Domain: fields[0]})
	}
	return entries, nil
}
