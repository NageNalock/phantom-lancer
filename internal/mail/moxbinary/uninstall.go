package moxbinary

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Uninstall removes the Phantom-controlled Mox install and its version
// sidecar.  It refuses to operate unless the version sidecar exists (i.e.
// the binary was installed by a prior call to Install from this package) –
// this ensures we never delete a PATH-discovered, OS-package, or manually
// placed mox binary.  (C4 corollary: we touch only what we wrote.)
//
// Safety checks performed (in order):
//  1. controlledDir must be non-empty.
//  2. `<controlledDir>/mox.version` must exist and be readable.  If it
//     does not, ErrNotControlled is returned.
//  3. (best-effort) `<controlledDir>/mox` must not be in use by a running
//     process.  If it is, ErrBinaryInUse is returned.  (Callers should
//     implement their own "running?" check via the supervisor package before
//     calling Uninstall; this is belt-and-suspenders.)
//  4. `mox` and `mox.version` are removed.  Any `mox.bak.*` backups
//     left behind by Install are also cleaned up.
//  5. controlledDir itself is NEVER removed – even if empty, the caller set
//     it up (via supervisor.EnsurePaths) and other subdirs may exist.
//
// The function does NOT stop a running Mox.  Callers are responsible for
// calling Supervisor.Stop() first.
func Uninstall(controlledDir string) error {
	_, err := UninstallWithResult(controlledDir, false)
	return err
}

// ForceUninstall behaves like Uninstall but skips the best-effort "is
// binary in use?" check.  Intended for the UI's "force uninstall" toggle
// after the operator has confirmed they know what they're doing.
//
// The sidecar-existence check (rule 2) is NEVER skipped – it is the
// defence-in-depth measure that guarantees we don't nuke an OS-package
// binary.  There is no way around it.
func ForceUninstall(controlledDir string) error {
	_, err := UninstallWithResult(controlledDir, true)
	return err
}

// UninstallResult is a richer return shape used by callers that want to
// show a summary of what was removed.
type UninstallResult struct {
	RemovedBinary      bool   `json:"removed_binary"`
	RemovedSidecar    bool   `json:"removed_sidecar"`
	BackupsRemoved    int    `json:"backups_removed"`
	ControlledDir     string `json:"controlled_dir"`
	UninstalledVersion string `json:"uninstalled_version"`
}

// UninstallWithResult is Uninstall with a detailed return.  Use it when the
// UI wants to show "removed v0.9.2 + 1 backup" banners.
//
// `force` skips the binary-in-use check only.
func UninstallWithResult(controlledDir string, force bool) (*UninstallResult, error) {
	if controlledDir == "" {
		return nil, fmt.Errorf("moxbinary: empty controlledDir")
	}

	sidecarPath := filepath.Join(controlledDir, sidecarFilename)
	binaryPath := filepath.Join(controlledDir, "mox")

	res := &UninstallResult{ControlledDir: controlledDir}

	// Check 1: sidecar MUST exist – no sidecar means we didn't install it.
	sc, err := readVersionSidecar(sidecarPath)
	if err != nil {
		return res, fmt.Errorf("moxbinary: read version sidecar: %w", err)
	}
	if sc == nil {
		return res, fmt.Errorf("%w: %s does not exist; refusing to uninstall a non-Phantom install",
			ErrNotControlled, sidecarPath)
	}
	res.UninstalledVersion = sc.Version

	// Check 2: running? (Linux-only best-effort.
	if !force {
		// binaryInUse walks /proc/<pid>/maps on Linux; on other OSes it
		// returns (false, err) which is fine – we don't want to
		// block uninstall on a probe noise.
		if inUse, ierr := binaryInUse(context.Background(), binaryPath); ierr == nil && inUse {
			return res, ErrBinaryInUse
		}
	}

	// Remove main binary.
	if err := os.Remove(binaryPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return res, fmt.Errorf("moxbinary: remove %s: %w", binaryPath, err)
		}
	} else {
		res.RemovedBinary = true
	}

	// Remove sidecar.
	if err := os.Remove(sidecarPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return res, fmt.Errorf("moxbinary: remove %s: %w", sidecarPath, err)
		}
	} else {
		res.RemovedSidecar = true
	}

	// Clean up mox.bak.<epoch> backups.
	entries, err := os.ReadDir(controlledDir)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "mox.bak.") {
				continue
			}
			// Defensive: only remove mox.bak.<digits> so a user-created
			// mox.bak.important file is never touched.
			suffix := name[len("mox.bak."):]
			if !isASCIIDigits(suffix) {
				continue
			}
			full := filepath.Join(controlledDir, name)
			if err := os.Remove(full); err == nil {
				res.BackupsRemoved++
			}
		}
	}

	return res, nil
}
