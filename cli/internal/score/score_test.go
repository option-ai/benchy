package score

import (
	"math"
	"testing"

	"github.com/option-ai/benchy/cli/internal/config"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestJudgeOverallWeighted(t *testing.T) {
	c := config.Default()
	sub := Subscores{TaskCompletion: 100, Correctness: 100, FeedbackAdherence: 0, ScopeDiscipline: 0}
	// weights: 0.40,0.30,0.20,0.10 -> (0.40+0.30)/1.0 *100 = 70
	got := JudgeOverall(sub, c.Rubric)
	if !approx(got, 70) {
		t.Fatalf("want 70, got %v", got)
	}
}

func TestBuildFailureCapsHard(t *testing.T) {
	c := config.Default()
	sub := Subscores{TaskCompletion: 90, Correctness: 90, FeedbackAdherence: 90, ScopeDiscipline: 90}
	gates := GateResult{Build: GateOutcome{Ran: true, Passed: false}}
	r := Compute("e", "m", sub, gates, c)
	// judge_overall 90, gate_factor capped at 0.30 -> 27
	if !approx(r.GateFactor, 0.30) {
		t.Fatalf("gate factor want 0.30, got %v", r.GateFactor)
	}
	if !approx(r.Composite, 27) {
		t.Fatalf("composite want 27, got %v", r.Composite)
	}
}

func TestTestFailureHalves(t *testing.T) {
	c := config.Default()
	sub := Subscores{TaskCompletion: 80, Correctness: 80, FeedbackAdherence: 80, ScopeDiscipline: 80}
	gates := GateResult{
		Build: GateOutcome{Ran: true, Passed: true},
		Test:  TestOutcome{Ran: true, Passed: false},
	}
	r := Compute("e", "m", sub, gates, c)
	if !approx(r.GateFactor, 0.5) {
		t.Fatalf("gate factor want 0.5, got %v", r.GateFactor)
	}
	if !approx(r.Composite, 40) {
		t.Fatalf("composite want 40, got %v", r.Composite)
	}
}

func TestCleanRunIsUngated(t *testing.T) {
	c := config.Default()
	sub := Subscores{TaskCompletion: 100, Correctness: 100, FeedbackAdherence: 100, ScopeDiscipline: 100}
	gates := GateResult{
		Build: GateOutcome{Ran: true, Passed: true},
		Test:  TestOutcome{Ran: true, Passed: true},
		Lint:  GateOutcome{Ran: true, Passed: true},
	}
	r := Compute("e", "m", sub, gates, c)
	if !approx(r.Composite, 100) {
		t.Fatalf("composite want 100, got %v", r.Composite)
	}
}

func TestLeaderboardRanks(t *testing.T) {
	rs := []Result{
		{Model: "a", Composite: 50},
		{Model: "a", Composite: 70},
		{Model: "b", Composite: 90},
	}
	lb := Leaderboard(rs)
	if lb[0].Model != "b" || !approx(lb[0].Score, 90) {
		t.Fatalf("expected b top at 90, got %+v", lb[0])
	}
	if lb[1].Model != "a" || !approx(lb[1].Score, 60) {
		t.Fatalf("expected a second at 60, got %+v", lb[1])
	}
}
