package httpapi

import (
	"net/http"

	"phantom-lancer/internal/authlimiter"
)

// This package previously defined the login backoff types in-place. They
// have moved to the shared internal/authlimiter package so both httpapi
// (admin web logins, unlocks, confirmations) and dockercontrol (registry
// basic auth) can use the same limiter without duplicating the state
// machine. The aliases below preserve the old names used throughout this
// package.

const (
	defaultLoginFailureThreshold = authlimiter.DefaultFailureThreshold
	loginFailureReset            = authlimiter.FailureReset
	loginBackoffBase             = authlimiter.BackoffBase
	loginBackoffMax              = authlimiter.BackoffMax
)

type (
	loginBackoff         = authlimiter.Backoff
	loginBackoffDecision = authlimiter.Decision
	loginBackoffEvent    = authlimiter.Event
)

func newLoginBackoff(threshold int) *authlimiter.Backoff {
	return authlimiter.NewBackoff(threshold)
}

func clientIP(r *http.Request) string {
	return authlimiter.ClientIP(r)
}
