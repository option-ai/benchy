package config

import "testing"

// The default ships auto-promote on at a near-pristine bar (a good default the
// user can tune), not off.
func TestDefaultAutoPromoteScore(t *testing.T) {
	if got := Default().AutoPromoteScore; got != 90 {
		t.Fatalf("Default().AutoPromoteScore = %d, want 90", got)
	}
}

func TestAgentDisabledRoundtrip(t *testing.T) {
	var c Config
	c.SetAgentDisabled("codex", true)
	c.SetAgentDisabled("opencode", true)
	if !c.AgentDisabled("codex") || !c.AgentDisabled("opencode") || c.AgentDisabled("claude-code") {
		t.Fatalf("unexpected state: %v", c.DisabledAgents)
	}
	c.SetAgentDisabled("codex", false)
	if c.AgentDisabled("codex") || !c.AgentDisabled("opencode") {
		t.Fatalf("re-enable broke state: %v", c.DisabledAgents)
	}
	c.SetAgentDisabled("opencode", true) // idempotent
	if len(c.DisabledAgents) != 1 {
		t.Fatalf("duplicate entries: %v", c.DisabledAgents)
	}
}
