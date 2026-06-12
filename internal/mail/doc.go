// Package mail implements the Phantom Lancer Mox sidecar control plane.
//
// It is the single owner of Mox lifecycle (binary install / start / stop /
// restart, supervisor, probes), Mox configuration (domains / accounts /
// aliases / certificates / delivery / queue / runtime), Mox credential
// lifecycle (account passwords via stdin pipe – never argv, never persisted),
// IMAP synchronisation, and full-text search over the local message index.
//
// Hard constraints – see docs/mail-mox-control-plane-wip.md §0:
//
//  1. Phantom never binds 80/443.
//  2. No reverse proxy integration – plain-text guidance only.
//  3. Phantom is the sole source of truth for system-level mail config
//     (accounts / domains / aliases / certs / queue / runtime).  External
//     edits to the Mox config file are detected as drift and the operator
//     is forced to choose one side – we never silently reconcile.
//  4. Mox binary is never auto-upgraded.  Only download / install /
//     uninstall / start / stop / restart are implemented.
//
// The independent MoxSupervisor lives in ./moxsupervisor and MUST NOT import
// or reuse internal/supervisor (which is the parent-process watchdog for
// Phantom itself, not a sidecar supervisor).
package mail
