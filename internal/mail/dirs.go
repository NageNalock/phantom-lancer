package mail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// moxSubDirs lists the directories we pre-create under MoxRoot() so that
// downstream modules (supervisor, certmanager, moxcli, probes) can assume
// them.  The list is intentionally small and Mox-specific – nothing that
// Mox itself creates on first boot is listed here.
var moxSubDirs = []string{
	"bin",     // controlled mox binary install
	"data",    // mox --data root
	"config",  // generated mox.conf + checkpoints + drift backups
	"logs",    // mox stdout/stderr + supervisor log
	"run",     // marker files, pidfile, unix sockets
	"backups", // phase 8: atomic tar.gz backups (exclude from data_full scope)
}

// ensureDirs creates moxRoot + moxSubDirs with 0700.  Existing directories
// are untouched (including their permissions – if an operator explicitly
// wants group-readable they can chmod themselves).
func (s *Service) ensureDirs(ctx context.Context) error {
	if s.moxRoot == "" {
		return fmt.Errorf("mail: moxRoot is empty")
	}
	dirs := make([]string, 0, 1+len(moxSubDirs))
	dirs = append(dirs, s.moxRoot)
	for _, d := range moxSubDirs {
		dirs = append(dirs, filepath.Join(s.moxRoot, d))
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}
