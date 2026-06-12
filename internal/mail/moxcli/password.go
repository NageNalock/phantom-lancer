package moxcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// SetAccountPassword runs `moxBinPath setaccountpassword <accountAddr>`
// and pipes the password through an os.Pipe so it never reaches argv,
// /proc/<pid>/cmdline, or any shell.
//
// C6 contract: the password bytes are written exactly once into the
// write end of the pipe and the write end is closed immediately after
// the write.  No logging, no length reporting, no prefix — nothing
// about the password may leave this function besides the pipe bytes
// consumed by the mox subprocess stdin.
func SetAccountPassword(
	ctx context.Context,
	moxBinPath string,
	accountAddr string,
	password []byte,
) error {
	// Construct the pipe before the command so neither end escapes.
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("moxcli: create password pipe: %w", err)
	}

	// Write the password ONCE in a goroutine so we cannot deadlock
	// with CombinedOutput (which drains both stdout+stderr before
	// returning).  The write end is closed unconditionally so the
	// child sees EOF on stdin.
	go func() {
		// Ignore write errors here: CombinedOutput below will
		// surface any child-side failure.  We deliberately do
		// not retain a reference to the error or the bytes.
		_, _ = w.Write(password)
		_ = w.Close()
	}()

	// Build the command with shell disabled.
	cmd := exec.CommandContext(ctx, moxBinPath,
		"setaccountpassword", accountAddr,
	)
	cmd.Stdin = r

	// Captured combined output is safe to include in the error
	// because `mox setaccountpassword` never echoes the password.
	combined, runErr := cmd.CombinedOutput()
	// Close the read end after CombinedOutput returns so the pipe
	// is fully released (also safe on double-close).
	_ = r.Close()

	if runErr != nil {
		return fmt.Errorf("mox setaccountpassword failed: %w: %s",
			runErr, string(combined))
	}
	return nil
}
