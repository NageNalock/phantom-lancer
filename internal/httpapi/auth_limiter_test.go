package httpapi

import (
	"net/http"
	"testing"
	"time"
)

func TestLoginBackoffTriggersForAccountAndIP(t *testing.T) {
	limiter := newLoginBackoff(defaultLoginFailureThreshold)
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

	for i := 0; i < defaultLoginFailureThreshold-1; i++ {
		if events := limiter.RecordFailure("Owner", "203.0.113.10", now.Add(time.Duration(i)*time.Second)); len(events) != 0 {
			t.Fatalf("RecordFailure() events before threshold = %#v", events)
		}
	}

	events := limiter.RecordFailure("owner", "203.0.113.10", now.Add(5*time.Second))
	if len(events) != 2 {
		t.Fatalf("RecordFailure() events = %d, want account and ip", len(events))
	}
	decision := limiter.Check("OWNER", "203.0.113.10", now.Add(6*time.Second))
	if !decision.Limited {
		t.Fatal("Check() did not limit after threshold")
	}
}

func TestLoginBackoffSuccessClearsOnlyAccount(t *testing.T) {
	limiter := newLoginBackoff(defaultLoginFailureThreshold)
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

	for i := 0; i < defaultLoginFailureThreshold; i++ {
		limiter.RecordFailure("owner", "203.0.113.10", now.Add(time.Duration(i)*time.Second))
	}
	limiter.RecordSuccess("owner")

	if decision := limiter.Check("owner", "", now.Add(10*time.Second)); decision.Limited {
		t.Fatal("account should be cleared after success")
	}
	if decision := limiter.Check("different", "203.0.113.10", now.Add(10*time.Second)); !decision.Limited || decision.Dimension != "ip" {
		t.Fatalf("ip backoff should remain after account success, got %#v", decision)
	}
}

func TestClientIPUsesRemoteAddrHost(t *testing.T) {
	request := &http.Request{RemoteAddr: "203.0.113.10:49152"}
	if got := clientIP(request); got != "203.0.113.10" {
		t.Fatalf("clientIP() = %q", got)
	}
}

func TestLoginBackoffUsesConfiguredThreshold(t *testing.T) {
	limiter := newLoginBackoff(2)
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

	if events := limiter.RecordFailure("owner", "203.0.113.10", now); len(events) != 0 {
		t.Fatalf("RecordFailure() first event = %#v", events)
	}
	if events := limiter.RecordFailure("owner", "203.0.113.10", now.Add(time.Second)); len(events) != 2 {
		t.Fatalf("RecordFailure() configured threshold events = %d, want 2", len(events))
	}
}
