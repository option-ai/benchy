package snapshot

import (
	"strings"
	"testing"
)

// A candidate's notes comment sits between the frontmatter and the prompts. It
// must round-trip: rendering it then parsing back must recover the prompts
// unchanged and never leak the notes into a prompt block.
func TestRenderNotesRoundTrip(t *testing.T) {
	s := &Snapshot{Prompts: []string{"first prompt", "second prompt"}}
	s.Title = "demo"
	s.Source = "auto"
	s.Notes = "<!-- benchy auto-capture\nverdict: weak (score 55)\n-->"

	raw, err := s.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(raw), "verdict: weak") {
		t.Errorf("notes not rendered:\n%s", raw)
	}
	if !strings.Contains(string(raw), "source: auto") {
		t.Errorf("source not rendered:\n%s", raw)
	}

	back, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(back.Prompts) != 2 {
		t.Fatalf("Prompts = %d %q, want 2", len(back.Prompts), back.Prompts)
	}
	if back.Prompts[0] != "first prompt" || back.Prompts[1] != "second prompt" {
		t.Errorf("prompts corrupted by notes: %q", back.Prompts)
	}
	for _, p := range back.Prompts {
		if strings.Contains(p, "verdict") {
			t.Errorf("notes leaked into prompt: %q", p)
		}
	}
}
