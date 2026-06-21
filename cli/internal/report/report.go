// Package report sends anonymous leaderboard rows to the global leaderboard
// at benchy.run when the user has opted in (config.report_results, default
// false). Only aggregate numbers leave the machine: model name, mean
// composite, run count, and the judge id. Prompts, diffs, repo names, and
// eval content are never sent.
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/option-ai/benchy/cli/internal/score"
)

// Endpoint is where rows are POSTed. Override with $BENCH_LEADERBOARD_URL
// (used by tests and self-hosters).
func Endpoint() string {
	if u := os.Getenv("BENCH_LEADERBOARD_URL"); u != "" {
		return u
	}
	return "https://benchy.run/api/results"
}

// Submission is the wire format for one run's aggregate rows.
type Submission struct {
	Judge string `json:"judge"`
	Rows  []Row  `json:"rows"`
}

type Row struct {
	Model string  `json:"model"`
	Score float64 `json:"score"`
	Runs  int     `json:"runs"`
}

// Send posts the leaderboard rows. Best-effort: short timeout, no retries —
// a failed report must never fail or slow down a benchy run materially.
func Send(ctx context.Context, judge string, rows []score.LeaderRow) error {
	if len(rows) == 0 {
		return nil
	}
	sub := Submission{Judge: judge}
	for _, r := range rows {
		sub.Rows = append(sub.Rows, Row{Model: r.Model, Score: r.Score, Runs: r.Runs})
	}
	body, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("leaderboard responded %s", resp.Status)
	}
	return nil
}
