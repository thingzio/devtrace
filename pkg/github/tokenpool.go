package github

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const tokenResetWindow = 50 * time.Minute // GitHub rate limits reset after 1 hour

// TokenPool manages a pool of GitHub API tokens using round-robin selection.
// Thread-safe. Supports marking tokens as exhausted after rate limit errors.
// Exhausted tokens auto-reset after the rate limit window (1 hour).
// Adapted from DevPulse pkg/data/ghutil/tokenpool.go.
type TokenPool struct {
	mu          sync.Mutex
	tokens      []string
	counts      []int
	exhausted   []bool
	exhaustedAt []time.Time
	current     int
}

// NewTokenPool creates a pool from one or more tokens. Tokens can be passed
// individually or as a single comma-separated string.
func NewTokenPool(tokens ...string) *TokenPool {
	var list []string
	for _, t := range tokens {
		for part := range strings.SplitSeq(t, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				list = append(list, part)
			}
		}
	}
	return &TokenPool{
		tokens:      list,
		counts:      make([]int, len(list)),
		exhausted:   make([]bool, len(list)),
		exhaustedAt: make([]time.Time, len(list)),
	}
}

// Token returns the next non-exhausted token in the round-robin rotation.
// Returns "" when all tokens are exhausted or the pool is empty.
func (p *TokenPool) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.tokens)
	if n == 0 {
		return ""
	}

	now := time.Now()
	for range n {
		idx := p.current
		p.current = (idx + 1) % n
		// Auto-reset tokens whose rate limit window has passed.
		if p.exhausted[idx] && now.Sub(p.exhaustedAt[idx]) > tokenResetWindow {
			p.exhausted[idx] = false
		}
		if !p.exhausted[idx] {
			p.counts[idx]++
			return p.tokens[idx]
		}
	}

	return ""
}

// Exhaust marks the given token as exhausted so Token() skips it.
func (p *TokenPool) Exhaust(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, t := range p.tokens {
		if t == token {
			p.exhausted[i] = true
			p.exhaustedAt[i] = time.Now()
			return
		}
	}
}

// ActiveCount returns the number of non-exhausted tokens.
func (p *TokenPool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	count := 0
	for i := range p.tokens {
		if !p.exhausted[i] || now.Sub(p.exhaustedAt[i]) > tokenResetWindow {
			count++
		}
	}
	return count
}

// Size returns the total number of tokens in the pool (including exhausted).
func (p *TokenPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.tokens)
}

// UsageCounts returns a copy of per-token call counts (indexed by pool position).
func (p *TokenPool) UsageCounts() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, len(p.counts))
	copy(out, p.counts)
	return out
}

// TokenQuota holds rate limit info for a single GitHub API token.
type TokenQuota struct {
	Index     int
	Limit     int
	Remaining int
	Reset     time.Time
	Error     string
}

// CheckQuotas calls the GitHub rate_limit API for each token in the pool.
// This endpoint is free (does not count against quota).
func (p *TokenPool) CheckQuotas(ctx context.Context) []TokenQuota {
	p.mu.Lock()
	tokens := make([]string, len(p.tokens))
	copy(tokens, p.tokens)
	p.mu.Unlock()

	quotas := make([]TokenQuota, len(tokens))
	for i, token := range tokens {
		quotas[i] = checkTokenRateLimit(ctx, token)
		quotas[i].Index = i
	}
	return quotas
}

// AggregateQuota returns the aggregate remaining percentage and earliest reset
// time across all tokens. Returns 100 if quotas is empty or all errored.
func AggregateQuota(quotas []TokenQuota) (pctRemaining int, earliestReset time.Time) {
	var totalLimit, totalRemaining int
	for _, q := range quotas {
		if q.Error != "" {
			continue
		}
		totalLimit += q.Limit
		totalRemaining += q.Remaining
		if !q.Reset.IsZero() && (earliestReset.IsZero() || q.Reset.Before(earliestReset)) {
			earliestReset = q.Reset
		}
	}
	if totalLimit == 0 {
		return 100, earliestReset
	}
	return (totalRemaining * 100) / totalLimit, earliestReset
}

var quotaClient = &http.Client{Timeout: 5 * time.Second}

func checkTokenRateLimit(ctx context.Context, token string) TokenQuota {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/rate_limit", nil)
	if err != nil {
		return TokenQuota{Error: "request error"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := quotaClient.Do(req)
	if err != nil {
		return TokenQuota{Error: "rate limit check failed"}
	}
	defer resp.Body.Close()

	var rl struct {
		Resources struct {
			Core struct {
				Limit     int   `json:"limit"`
				Remaining int   `json:"remaining"`
				Reset     int64 `json:"reset"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return TokenQuota{Error: "decode error"}
	}

	core := rl.Resources.Core
	return TokenQuota{
		Limit:     core.Limit,
		Remaining: core.Remaining,
		Reset:     time.Unix(core.Reset, 0).UTC(),
	}
}
