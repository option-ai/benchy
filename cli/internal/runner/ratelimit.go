package runner

import (
	"context"
	"strings"
	"time"
)

// rateLimitMarkers are substrings (lowercased match) that identify a failure
// as rate-limiting/quota rather than a real agent error. CLIs surface these in
// stderr/stdout text; none expose a structured quota API to callers.
var rateLimitMarkers = []string{
	"rate limit", "rate_limit", "ratelimit",
	"429", "too many requests",
	"overloaded", "quota", "usage limit", "exhausted",
	"capacity", "try again later",
}

// looksRateLimited reports whether an agent failure smells like RPM/TPM/quota.
func looksRateLimited(output string, err error) bool {
	hay := strings.ToLower(output)
	if err != nil {
		hay += " " + strings.ToLower(err.Error())
	}
	for _, m := range rateLimitMarkers {
		if strings.Contains(hay, m) {
			return true
		}
	}
	return false
}

// backoff returns the wait before retry attempt n (1-based): 30s, 90s, 180s...
func backoff(n int) time.Duration {
	switch n {
	case 1:
		return 30 * time.Second
	case 2:
		return 90 * time.Second
	default:
		return 180 * time.Second
	}
}

// sleepCtx waits d or until ctx is done, returning false if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
