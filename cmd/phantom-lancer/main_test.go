package main

import "testing"

func TestStockV2AgentMCPURLUsesLoopbackForWildcardAddr(t *testing.T) {
	got := stockV2AgentMCPURL("http", "0.0.0.0:18080")
	want := "http://127.0.0.1:18080/api/stockv2/agent/mcp"
	if got != want {
		t.Fatalf("mcp url = %q, want %q", got, want)
	}
}

func TestStockV2AgentMCPURLKeepsExplicitHost(t *testing.T) {
	got := stockV2AgentMCPURL("https", "localhost:18443")
	want := "https://localhost:18443/api/stockv2/agent/mcp"
	if got != want {
		t.Fatalf("mcp url = %q, want %q", got, want)
	}
}
