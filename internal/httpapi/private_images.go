package httpapi

import (
	"sync"
	"time"
)

const privateImageUnlockTTL = 15 * time.Minute

type privateImageAccess struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

func newPrivateImageAccess() *privateImageAccess {
	return &privateImageAccess{sessions: make(map[string]time.Time)}
}

func (a *privateImageAccess) Unlock(sessionID string, now time.Time) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	expiresAt := now.Add(privateImageUnlockTTL)
	a.sessions[sessionID] = expiresAt
	return expiresAt
}

func (a *privateImageAccess) Lock(sessionID string) {
	a.mu.Lock()
	delete(a.sessions, sessionID)
	a.mu.Unlock()
}

func (a *privateImageAccess) IsUnlocked(sessionID string, now time.Time) (bool, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	expiresAt := a.sessions[sessionID]
	if expiresAt.IsZero() {
		return false, time.Time{}
	}
	if now.After(expiresAt) {
		delete(a.sessions, sessionID)
		return false, time.Time{}
	}
	return true, expiresAt
}
