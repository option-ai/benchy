package transcript

import (
	"strings"
	"testing"
)

// fixture mirrors the real Claude Code JSONL shape: string and array user
// content, a slash-command echo, skill/reminder noise, an MCP tool_use, a code
// edit, and a git Bash call.
const fixture = `
{"type":"ai-title","title":"fix dns for tracked domains"}
{"type":"user","timestamp":"2026-06-18T10:00:00.000Z","gitBranch":"fix/dns","cwd":"/repo","message":{"content":"even for tracked domains we should still be able to change their dns settings"}}
{"type":"user","message":{"content":"<command-name>/ship</command-name>"}}
{"type":"user","message":{"content":[{"type":"text","text":"<system-reminder>be nice</system-reminder>"}]}}
{"type":"user","message":{"content":[{"type":"text","text":"Base directory for this skill: /x"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/dns.ts"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"cd /repo && git diff --stat"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__posthog__exec","input":{}}]}}
{"type":"user","timestamp":"2026-06-18T10:20:00.000Z","gitBranch":"fix/dns","message":{"content":"go on"}}
malformed line that is not json
`

func TestParse(t *testing.T) {
	s, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Title != "fix dns for tracked domains" {
		t.Errorf("Title = %q", s.Title)
	}
	if len(s.Prompts) != 2 { // the real prompt + "go on"; noise/command excluded
		t.Fatalf("Prompts = %d %q, want 2", len(s.Prompts), s.Prompts)
	}
	if !strings.Contains(s.Prompts[0], "tracked domains") {
		t.Errorf("first prompt = %q", s.Prompts[0])
	}
	if got := s.Commands; len(got) != 1 || got[0] != "ship" {
		t.Errorf("Commands = %v, want [ship]", got)
	}
	if len(s.MCPTools) != 1 {
		t.Errorf("MCPTools = %v, want 1", s.MCPTools)
	}
	if len(s.EditedFiles) != 1 || s.EditedFiles[0] != "/repo/dns.ts" {
		t.Errorf("EditedFiles = %v", s.EditedFiles)
	}
	if s.GitMutations != 1 {
		t.Errorf("GitMutations = %d, want 1", s.GitMutations)
	}
	if s.Branches == nil || s.Branches[0] != "fix/dns" {
		t.Errorf("Branches = %v", s.Branches)
	}
	if s.FirstTS == "" || s.LastTS == "" {
		t.Errorf("timestamps not captured: %q %q", s.FirstTS, s.LastTS)
	}
	if s.Cwd != "/repo" {
		t.Errorf("Cwd = %q", s.Cwd)
	}
}

// "AQID" is base64 for bytes {1,2,3}.
const imgFixture = `
{"type":"user","gitBranch":"ui","message":{"content":[{"type":"text","text":"match this mockup please, fix the spacing and the header colors"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AQID"}}]}}
`

func TestParseExtractsEmbeddedImage(t *testing.T) {
	s, err := Parse(strings.NewReader(imgFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(s.Images) != 1 {
		t.Fatalf("Images = %d, want 1", len(s.Images))
	}
	if s.Images[0].Ext != ".png" {
		t.Errorf("Ext = %q, want .png", s.Images[0].Ext)
	}
	if string(s.Images[0].Data) != "\x01\x02\x03" {
		t.Errorf("Data = %v, want [1 2 3]", s.Images[0].Data)
	}
	// the text alongside the image is still captured as a prompt
	if len(s.Prompts) != 1 || !strings.Contains(s.Prompts[0], "mockup") {
		t.Errorf("Prompts = %q", s.Prompts)
	}
}

// This fixture is the same session; it should be rejected for MCP dependence
// even though it has a clean prompt and a real diff.
func TestParseThenEvaluate(t *testing.T) {
	s, _ := Parse(strings.NewReader(fixture))
	r := Evaluate(s)
	if r.Accept {
		t.Errorf("expected reject (MCP dependence), got accept score %d", r.Score)
	}
}
