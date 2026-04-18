package github

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const tokenResetWindow = 50 * time.Minute // GitHub rate limits reset after 1 hour

const tokenExpiryBuffer = 5 * time.Minute

// PoolEntry describes a token source for the pool.
type PoolEntry struct {
	InstallationID int64
	Label          string // target login (org/user) or "PAT"
	Token          string
	ExpiresAt      time.Time // zero means never expires (PAT)
}

type poolEntry struct {
	installationID int64
	label          string
	token          string
	expiresAt      time.Time
}

// TokenPool manages a pool of GitHub API tokens using round-robin selection.
// Thread-safe. Supports marking tokens as exhausted after rate limit errors.
// Exhausted tokens auto-reset after the rate limit window (1 hour).
// Adapted from DevPulse pkg/data/ghutil/tokenpool.go.
type TokenPool struct {
	mu          sync.Mutex
	entries     []poolEntry
	counts      []int
	exhausted   []bool
	exhaustedAt []time.Time
	current     int
	refreshCh   chan<- struct{} // optional; signaled when a near-expiry token is selected
}

// NewTokenPool creates a pool from one or more tokens. Tokens can be passed
// individually or as a single comma-separated string.
func NewTokenPool(tokens ...string) *TokenPool {
	var list []poolEntry
	for _, t := range tokens {
		for part := range strings.SplitSeq(t, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				list = append(list, poolEntry{token: part})
			}
		}
	}
	return &TokenPool{
		entries:     list,
		counts:      make([]int, len(list)),
		exhausted:   make([]bool, len(list)),
		exhaustedAt: make([]time.Time, len(list)),
	}
}

// NewTokenPoolFromEntries creates a pool from typed entries with metadata.
func NewTokenPoolFromEntries(entries []PoolEntry) *TokenPool {
	list := make([]poolEntry, len(entries))
	for i, e := range entries {
		list[i] = poolEntry{
			installationID: e.InstallationID,
			label:          e.Label,
			token:          e.Token,
			expiresAt:      e.ExpiresAt,
		}
	}
	return &TokenPool{
		entries:     list,
		counts:      make([]int, len(list)),
		exhausted:   make([]bool, len(list)),
		exhaustedAt: make([]time.Time, len(list)),
	}
}

// Replace atomically swaps the pool entries. Resets cursor, counts, and exhaustion state.
func (p *TokenPool) Replace(entries []PoolEntry) {
	pe := make([]poolEntry, len(entries))
	for i, e := range entries {
		pe[i] = poolEntry{
			installationID: e.InstallationID,
			label:          e.Label,
			token:          e.Token,
			expiresAt:      e.ExpiresAt,
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.entries = pe
	p.counts = make([]int, len(pe))
	p.exhausted = make([]bool, len(pe))
	p.exhaustedAt = make([]time.Time, len(pe))
	p.current = 0
}

// SetRefreshCh sets a channel that Token() will signal (non-blocking) when it
// selects a near-expiry token, allowing the refresh goroutine to re-mint early.
func (p *TokenPool) SetRefreshCh(ch chan<- struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshCh = ch
}

// Token returns the next non-exhausted token in the round-robin rotation.
// Returns "" when all tokens are exhausted or the pool is empty.
// Entries with a non-zero ExpiresAt that falls within tokenExpiryBuffer are skipped.
func (p *TokenPool) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.entries)
	if n == 0 {
		return ""
	}

	now := time.Now()
	needsRefresh := false
	for range n {
		idx := p.current
		p.current = (idx + 1) % n
		// Auto-reset tokens whose rate limit window has passed.
		if p.exhausted[idx] && now.Sub(p.exhaustedAt[idx]) > tokenResetWindow {
			p.exhausted[idx] = false
		}
		if p.exhausted[idx] {
			continue
		}
		entry := p.entries[idx]
		// Use near-expiry tokens but flag that a refresh is needed.
		if !entry.expiresAt.IsZero() && now.Add(tokenExpiryBuffer).After(entry.expiresAt) {
			needsRefresh = true
		}
		p.counts[idx]++
		if needsRefresh && p.refreshCh != nil {
			select {
			case p.refreshCh <- struct{}{}:
			default:
			}
		}
		return entry.token
	}

	return ""
}

// Exhaust marks the given token as exhausted so Token() skips it.
func (p *TokenPool) Exhaust(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, e := range p.entries {
		if e.token == token {
			p.exhausted[i] = true
			p.exhaustedAt[i] = time.Now()
			slog.Warn("token exhausted", "label", e.label)
			return
		}
	}
}

// ActiveCount returns the number of tokens that are usable (not exhausted and not expired).
func (p *TokenPool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	count := 0
	for i, e := range p.entries {
		exhausted := p.exhausted[i] && now.Sub(p.exhaustedAt[i]) <= tokenResetWindow
		expired := !e.expiresAt.IsZero() && now.After(e.expiresAt)
		if !exhausted && !expired {
			count++
		}
	}
	return count
}

// Size returns the total number of tokens in the pool (including exhausted).
func (p *TokenPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// UsageCounts returns a copy of per-token call counts (indexed by pool position).
func (p *TokenPool) UsageCounts() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, len(p.counts))
	copy(out, p.counts)
	return out
}

// NeedsRefresh returns true if any installation token is within the expiry buffer.
func (p *TokenPool) NeedsRefresh() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for _, e := range p.entries {
		if !e.expiresAt.IsZero() && now.Add(tokenExpiryBuffer).After(e.expiresAt) {
			return true
		}
	}
	return false
}

// Labels returns the label for each entry in pool order.
func (p *TokenPool) Labels() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	labels := make([]string, len(p.entries))
	for i, e := range p.entries {
		labels[i] = e.label
	}
	return labels
}

// TokenQuota holds rate limit info for a single GitHub API token.
type TokenQuota struct {
	Index          int
	Label          string
	InstallationID int64
	Limit          int
	Remaining      int
	Reset          time.Time
	Error          string
}

// CheckQuotas calls the GitHub rate_limit API for each token in the pool.
// This endpoint is free (does not count against quota).
func (p *TokenPool) CheckQuotas(ctx context.Context) []TokenQuota {
	p.mu.Lock()
	snapshot := make([]poolEntry, len(p.entries))
	copy(snapshot, p.entries)
	p.mu.Unlock()

	quotas := make([]TokenQuota, len(snapshot))
	for i, entry := range snapshot {
		quotas[i] = checkTokenRateLimit(ctx, entry.token)
		quotas[i].Index = i
		quotas[i].Label = entry.label
		quotas[i].InstallationID = entry.installationID
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
