package transcript

import "testing"

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name       string
		sess       *Session
		wantAccept bool
		wantVerd   Verdict
	}{
		{
			name: "clean single coding task",
			sess: &Session{
				Prompts:      []string{"the domains setup flow is broken, fix the DNS record cleanup so tracked domains can still be edited"},
				Branches:     []string{"fix/dns"},
				EditedFiles:  []string{"a.ts", "b.ts"},
				GitMutations: 3,
			},
			wantAccept: true, wantVerd: VerdictGood,
		},
		{
			name:       "no prompts (skill-driven)",
			sess:       &Session{Commands: []string{"log-autoimprove"}, MCPTools: []string{"mcp__posthog__exec"}},
			wantAccept: false, wantVerd: VerdictReject,
		},
		{
			name: "mcp dependence",
			sess: &Session{
				Prompts:  []string{"make discoverSourceCandidates cheaper and reduce the error rate substantially please"},
				Branches: []string{"perf"}, MCPTools: []string{"mcp__plugin_posthog_posthog__exec"},
			},
			wantAccept: false, wantVerd: VerdictReject,
		},
		{
			name: "non-portable slash command",
			sess: &Session{
				Prompts:  []string{"map our entire network and find ranking opportunities across all tenant sites"},
				Branches: []string{"seo"}, Commands: []string{"seo", "ship"},
			},
			wantAccept: false, wantVerd: VerdictReject,
		},
		{
			name: "harmless commands only are fine",
			sess: &Session{
				Prompts:  []string{"rework the tenant pages to lean on BTC and Trustpilot signals throughout"},
				Branches: []string{"tenant"}, Commands: []string{"ship", "rename", "config"},
				EditedFiles: []string{"page.astro"}, GitMutations: 2,
			},
			wantAccept: true, wantVerd: VerdictGood,
		},
		{
			name: "egregious branch sprawl is rejected",
			sess: &Session{
				Prompts:  []string{"optimize the expensive websearch path and fix the geo backfill targeting"},
				Branches: []string{"a", "b", "c", "d", "e"}, EditedFiles: []string{"x.ts"},
			},
			wantAccept: false, wantVerd: VerdictReject,
		},
		{
			name: "two branches is a weak warning, still captured",
			sess: &Session{
				Prompts:     []string{"add a DNS editing path for tracked domains so operators can change their records"},
				Branches:    []string{"chore/drain", "fix/dns"},
				EditedFiles: []string{"dns.ts"}, GitMutations: 4,
			},
			wantAccept: true, wantVerd: VerdictWeak,
		},
		{
			name: "only conversational steering",
			sess: &Session{
				Prompts:  []string{"go on", "launch it now", "is it finished?", "yes ure"},
				Branches: []string{"ops"},
			},
			wantAccept: false, wantVerd: VerdictReject,
		},
		{
			name: "image path-only reference is kept (bytes gone, light penalty)",
			sess: &Session{
				Prompts:  []string{"fix the layout shown in Screenshot 2026-06-15.png so the header stops overlapping"},
				Branches: []string{"ui"}, EditedFiles: []string{"h.css"}, GitMutations: 1,
			},
			wantAccept: true, wantVerd: VerdictGood,
		},
		{
			name: "embedded image is captured, no penalty",
			sess: &Session{
				Prompts:  []string{"make the dashboard header match the mockup I pasted, fixing spacing and colors"},
				Branches: []string{"ui"}, EditedFiles: []string{"h.css"}, GitMutations: 1,
				Images: []Image{{Ext: ".png", Data: []byte{1, 2, 3}}},
			},
			wantAccept: true, wantVerd: VerdictGood,
		},
		{
			name: "depends on prior session",
			sess: &Session{
				Prompts:  []string{"Continue from where you left off and finish the remaining migration steps"},
				Branches: []string{"mig"}, EditedFiles: []string{"m.ts"},
			},
			wantAccept: false, wantVerd: VerdictReject,
		},
		{
			name: "answer task, no diff => weak but accepted",
			sess: &Session{
				Prompts:  []string{"how much work would it be to migrate this Astro app to Next.js and deploy on Cloudflare?"},
				Branches: []string{"scoping"},
			},
			wantAccept: true, wantVerd: VerdictWeak,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.sess)
			if got.Accept != tc.wantAccept {
				t.Errorf("Accept = %v, want %v (reasons: %v)", got.Accept, tc.wantAccept, got.Reasons)
			}
			if got.Verdict != tc.wantVerd {
				t.Errorf("Verdict = %q, want %q (score %d, reasons: %v)", got.Verdict, tc.wantVerd, got.Score, got.Reasons)
			}
			if !tc.wantAccept && len(got.Reasons) == 0 {
				t.Errorf("rejected session must report at least one reason")
			}
		})
	}
}
