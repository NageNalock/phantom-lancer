package moxcli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// AccountCreate runs `mox account add <email>` and feeds the password
// via stdin pipe (never argv).
func (r *Runner) AccountCreate(ctx context.Context, a AccountEntry, password string) (bool, string, error) {
	args := []string{"add", a.Email}
	if a.DisplayName != "" {
		args = append(args, "displayname="+a.DisplayName)
	}
	if a.Role != "" {
		args = append(args, "role="+a.Role)
	}
	stdout, stderr, code, err := r.runWithStdinPipe(ctx, "account", args, func(w io.WriteCloser) error {
		if _, werr := fmt.Fprintln(w, password); werr != nil {
			return werr
		}
		// Some variants expect the password twice.
		_, werr := fmt.Fprintln(w, password)
		return werr
	})
	out := stdout + "\n" + stderr
	return code == 0 && err == nil, out, err
}

// AccountSetPassword runs `mox setaccountpassword <email>` with password piped.
func (r *Runner) AccountSetPassword(ctx context.Context, email string, password string) (bool, string, error) {
	stdout, stderr, code, err := r.runWithStdinPipe(ctx, "setaccountpassword", []string{email}, func(w io.WriteCloser) error {
		if _, werr := fmt.Fprintln(w, password); werr != nil {
			return werr
		}
		_, werr := fmt.Fprintln(w, password)
		return werr
	})
	out := stdout + "\n" + stderr
	return code == 0 && err == nil, out, err
}

// AccountDelete runs `mox account remove <email>`.
func (r *Runner) AccountDelete(ctx context.Context, email string) (bool, string, error) {
	stdout, stderr, code, err := r.run(ctx, "account", []string{"remove", email})
	out := stdout + "\n" + stderr
	return code == 0 && err == nil, out, err
}

// AccountList runs `mox account list` and parses best-effort.
func (r *Runner) AccountList(ctx context.Context) ([]AccountEntry, error) {
	stdout, _, _, err := r.run(ctx, "account", []string{"list"})
	if err != nil {
		return nil, err
	}
	var entries []AccountEntry
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
		entries = append(entries, AccountEntry{Email: fields[0]})
	}
	return entries, nil
}
