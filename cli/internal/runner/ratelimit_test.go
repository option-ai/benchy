package runner

import (
	"errors"
	"testing"
)

func TestLooksRateLimited(t *testing.T) {
	cases := []struct {
		out  string
		err  error
		want bool
	}{
		{"Error: 429 Too Many Requests", nil, true},
		{"upstream says: rate limit exceeded, retry later", nil, true},
		{"You have hit your usage limit for this period.", nil, true},
		{"model overloaded, please retry", nil, true},
		{"", errors.New("exit status 1: quota exceeded"), true},
		{"panic: nil pointer dereference", nil, false},
		{"build failed: syntax error", errors.New("exit status 2"), false},
	}
	for _, c := range cases {
		if got := looksRateLimited(c.out, c.err); got != c.want {
			t.Errorf("looksRateLimited(%q, %v) = %v, want %v", c.out, c.err, got, c.want)
		}
	}
}
