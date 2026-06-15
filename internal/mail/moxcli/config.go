package moxcli

import (
	"bufio"
	"context"
	"strings"
)

// ConfigTest runs `mox config test` and splits the output into
// errors / warnings by line prefix.
func (r *Runner) ConfigTest(ctx context.Context) (*ConfigTestResult, error) {
	stdout, stderr, exitCode, err := r.run(ctx, "config", []string{"test"})
	out := stdout + "\n" + stderr
	res := &ConfigTestResult{
		OK:     exitCode == 0 && err == nil,
		Output: out,
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "error"):
			res.Errors = append(res.Errors, line)
		case strings.Contains(lower, "warn"):
			res.Warnings = append(res.Warnings, line)
		}
	}
	return res, nil
}

// ConfigList runs `mox config list` and does a shallow `Key: Value` parse.
// Section header lines (no colon, left-aligned) introduce a nested
// ParsedConfig under the section key.
func (r *Runner) ConfigList(ctx context.Context) (ParsedConfig, error) {
	stdout, _, _, err := r.run(ctx, "config", []string{"list"})
	if err != nil {
		return nil, err
	}
	root := make(ParsedConfig)
	var current ParsedConfig = root
	var sectionStack []ParsedConfig

	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Section header (no colon, left-aligned).
		if line[0] != ' ' && line[0] != '\t' && !strings.Contains(line, ":") {
			name := strings.TrimSpace(line)
			next := make(ParsedConfig)
			root[name] = next
			current = next
			sectionStack = append(sectionStack, next)
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		idx := strings.Index(trimmed, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		current[key] = val
	}
	_ = sectionStack
	return root, nil
}
