package cmd

import (
	"testing"

	"github.com/option-ai/benchy/cli/internal/config"
	"github.com/option-ai/benchy/cli/internal/transcript"
)

func TestCaptureDest(t *testing.T) {
	cand, snap := "/x/candidates/auto-abc.md", "/x/snapshots/auto-abc.md"
	tests := []struct {
		name      string
		threshold int
		score     int
		wantDest  string
		wantProm  bool
	}{
		{"auto-promote off (0) keeps everything queued", 0, 100, cand, false},
		{"score below threshold → queue", 90, 89, cand, false},
		{"score at threshold → promote", 90, 90, snap, true},
		{"score above threshold → promote", 70, 100, snap, true},
		{"weak score below a high bar → queue", 90, 65, cand, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{AutoPromoteScore: tc.threshold}
			dest, prom := captureDest(cfg, transcript.Result{Score: tc.score}, cand, snap)
			if dest != tc.wantDest || prom != tc.wantProm {
				t.Errorf("captureDest(thr=%d, score=%d) = (%s, %v), want (%s, %v)",
					tc.threshold, tc.score, dest, prom, tc.wantDest, tc.wantProm)
			}
		})
	}
}
