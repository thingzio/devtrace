package github

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// tokenResetFallback is the default reset window when GitHub does not return
// a Reset header (or it's malformed). The real cap is 1 hour; we expire a
// little early so we re-probe before GitHub does.
const tokenResetFallback = 50 * time.Minute

const tokenExpiryBuffer = 5 * time.Minute

// permanentInvalidationUntil is the "until" sentinel used to mark a PAT
// entry permanently dead after a 401. A PAT cannot be re-minted, so
// retrying it after a timeout would only burn latency. ~10 years out is
// effectively forever for any running process.
var permanentInvalidationUntil = time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)

// invalidationRingCap caps the in-memory ring of recent invalidation
// events that the admin dashboard surfaces. Sized for "noisy" hours
// without unbounded growth; oldest entries roll off.
const invalidationRingCap = 64

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
// Thread-safe. Supports marking tokens as exhausted after rate limit errors;
// each exhaustion records the actual reset time GitHub returned (or a
// fallback) so the token only re-enters rotation when it is genuinely usable.
// Adapted from DevPulse pkg/data/ghutil/tokenpool.go.
type TokenPool struct {
	mu             sync.Mutex
	entries        []poolEntry
	counts         []int
	exhausted      []bool
	exhaustedUntil []time.Time
	current        int
	refreshCh      chan<- struct{} // optional; signaled when a near-expiry token is selected

	// invalidations is a bounded ring of recent 401 events for the admin
	// dashboard. Survives Replace() so operators can still see what
	// happened in the last hour even after a refresh.
	invalidations []InvalidationEvent
}

// InvalidationEvent records that a token was 401'd and removed from
// rotation. Exposed via RecentInvalidations for the admin dashboard.
type InvalidationEvent struct {
	At             time.Time
	Label          string
	InstallationID int64
	Permanent      bool
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
		entries:        list,
		counts:         make([]int, len(list)),
		exhausted:      make([]bool, len(list)),
		exhaustedUntil: make([]time.Time, len(list)),
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
		entries:        list,
		counts:         make([]int, len(list)),
		exhausted:      make([]bool, len(list)),
		exhaustedUntil: make([]time.Time, len(list)),
	}
}

// Replace atomically swaps the pool entries. Resets cursor, counts, and
// exhaustion state. Returns the set of token strings that were dropped so
// callers can invalidate any per-token caches (e.g., PoolClient's *gh.Client
// memoization). The returned slice contains only tokens that no longer
// appear in the new entries — tokens that survived the swap stay valid.
func (p *TokenPool) Replace(entries []PoolEntry) []string {
	pe := make([]poolEntry, len(entries))
	survivors := make(map[string]bool, len(entries))
	for i, e := range entries {
		pe[i] = poolEntry{
			installationID: e.InstallationID,
			label:          e.Label,
			token:          e.Token,
			expiresAt:      e.ExpiresAt,
		}
		survivors[e.Token] = true
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var dropped []string
	for _, old := range p.entries {
		if !survivors[old.token] {
			dropped = append(dropped, old.token)
		}
	}

	p.entries = pe
	p.counts = make([]int, len(pe))
	p.exhausted = make([]bool, len(pe))
	p.exhaustedUntil = make([]time.Time, len(pe))
	p.current = 0
	return dropped
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
		// Auto-reset tokens whose rate-limit reset time has passed.
		if p.exhausted[idx] && now.After(p.exhaustedUntil[idx]) {
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

// Exhaust marks the given token as exhausted so Token() skips it until
// resetAt. A zero resetAt falls back to tokenResetFallback from now; a
// resetAt in the past is treated as the fallback (defensive against clock
// skew between this host and GitHub).
func (p *TokenPool) Exhaust(token string, resetAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	until := resetAt
	if until.IsZero() || !until.After(now) {
		until = now.Add(tokenResetFallback)
	}

	for i, e := range p.entries {
		if e.token == token {
			p.exhausted[i] = true
			p.exhaustedUntil[i] = until
			slog.Warn("token exhausted", "label", e.label, "until", until.Format(time.RFC3339))
			return
		}
	}
}

// InvalidateAuth marks the given token as exhausted in response to a 401
// Bad credentials response from GitHub. Distinct log/path from Exhaust so
// operators can distinguish revoked/rotated credentials from rate-limit
// backoff.
//
// Installation tokens (installationID != 0) get tokenResetFallback so they
// re-enter rotation after roughly the window an installation token would
// naturally expire — by then the refresh goroutine will have re-minted
// them. PAT entries (installationID == 0) get a far-future until so they
// are permanently disabled until the process restarts: a PAT cannot be
// re-minted and retrying a revoked one only burns latency.
func (p *TokenPool) InvalidateAuth(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, e := range p.entries {
		if e.token == token {
			now := time.Now()
			until := now.Add(tokenResetFallback)
			permanent := e.installationID == 0
			if permanent {
				until = permanentInvalidationUntil
			}
			p.exhausted[i] = true
			p.exhaustedUntil[i] = until
			p.recordInvalidationLocked(InvalidationEvent{
				At:             now,
				Label:          e.label,
				InstallationID: e.installationID,
				Permanent:      permanent,
			})
			slog.Warn("token invalidated",
				"label", e.label,
				"installation_id", e.installationID,
				"reason", "auth_failure",
				"permanent", permanent,
				"until", until.Format(time.RFC3339))
			return
		}
	}
}

// RecentInvalidations returns events newer than since, oldest first. Pass
// a zero time to receive every event still in the ring. Safe for the
// admin dashboard to call without coordinating with the refresh loop.
func (p *TokenPool) RecentInvalidations(since time.Time) []InvalidationEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]InvalidationEvent, 0, len(p.invalidations))
	for _, ev := range p.invalidations {
		if since.IsZero() || ev.At.After(since) {
			out = append(out, ev)
		}
	}
	return out
}

// recordInvalidationLocked appends an event to the ring, dropping the
// oldest entry when the cap is reached. Caller must hold p.mu.
func (p *TokenPool) recordInvalidationLocked(ev InvalidationEvent) {
	if len(p.invalidations) >= invalidationRingCap {
		p.invalidations = append(p.invalidations[1:], ev)
		return
	}
	p.invalidations = append(p.invalidations, ev)
}

// SignalRefresh nudges the refresh goroutine to re-mint installation
// tokens, if a refresh channel has been registered. Non-blocking: if the
// channel already has a pending signal, this is a no-op. Used by callers
// that detect a poisoned token (e.g., auth failure) and want fresh
// credentials minted ahead of the periodic tick.
func (p *TokenPool) SignalRefresh() {
	p.mu.Lock()
	ch := p.refreshCh
	p.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// ActiveCount returns the number of tokens that are usable (not exhausted and not expired).
func (p *TokenPool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	count := 0
	for i, e := range p.entries {
		exhausted := p.exhausted[i] && !now.After(p.exhaustedUntil[i])
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

// TokenQuota holds rate limit info for a single GitHub API token. Limit /
// Remaining / Reset describe the core (REST) family; the Search* and
// GraphQL* fields cover the secondary families that the scoring path
// actually burns through (3 search calls per signal fetch, GraphQL for
// security credits). Empty values for a family mean GitHub didn't return
// it (rare; older endpoints).
type TokenQuota struct {
	Index            int
	Label            string
	InstallationID   int64
	Limit            int
	Remaining        int
	Reset            time.Time
	SearchLimit      int
	SearchRemaining  int
	SearchReset      time.Time
	GraphQLLimit     int
	GraphQLRemaining int
	GraphQLReset     time.Time
	Error            string
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
// time across all tokens, for the core (REST) rate-limit family. Returns
// 100 if quotas is empty or all errored.
func AggregateQuota(quotas []TokenQuota) (pctRemaining int, earliestReset time.Time) {
	return aggregate(quotas, func(q TokenQuota) (int, int, time.Time) {
		return q.Limit, q.Remaining, q.Reset
	})
}

// AggregateSearchQuota mirrors AggregateQuota for the search-API rate-limit
// family. The scoring path issues three search calls per contributor
// (merged/closed/recent PRs) so the search quota is exhausted long before
// the core quota under load.
func AggregateSearchQuota(quotas []TokenQuota) (pctRemaining int, earliestReset time.Time) {
	return aggregate(quotas, func(q TokenQuota) (int, int, time.Time) {
		return q.SearchLimit, q.SearchRemaining, q.SearchReset
	})
}

// AggregateGraphQLQuota mirrors AggregateQuota for the GraphQL rate-limit
// family. Used by enrichment paths that fetch GHSA security credits.
func AggregateGraphQLQuota(quotas []TokenQuota) (pctRemaining int, earliestReset time.Time) {
	return aggregate(quotas, func(q TokenQuota) (int, int, time.Time) {
		return q.GraphQLLimit, q.GraphQLRemaining, q.GraphQLReset
	})
}

func aggregate(quotas []TokenQuota, pick func(TokenQuota) (int, int, time.Time)) (int, time.Time) {
	var totalLimit, totalRemaining int
	var earliestReset time.Time
	for _, q := range quotas {
		if q.Error != "" {
			continue
		}
		limit, remaining, reset := pick(q)
		totalLimit += limit
		totalRemaining += remaining
		if !reset.IsZero() && (earliestReset.IsZero() || reset.Before(earliestReset)) {
			earliestReset = reset
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
	defer func() {
		// Drain any remaining body so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	var rl struct {
		Resources struct {
			Core    rateLimitFamily `json:"core"`
			Search  rateLimitFamily `json:"search"`
			GraphQL rateLimitFamily `json:"graphql"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return TokenQuota{Error: "decode error"}
	}

	core := rl.Resources.Core
	return TokenQuota{
		Limit:            core.Limit,
		Remaining:        core.Remaining,
		Reset:            time.Unix(core.Reset, 0).UTC(),
		SearchLimit:      rl.Resources.Search.Limit,
		SearchRemaining:  rl.Resources.Search.Remaining,
		SearchReset:      time.Unix(rl.Resources.Search.Reset, 0).UTC(),
		GraphQLLimit:     rl.Resources.GraphQL.Limit,
		GraphQLRemaining: rl.Resources.GraphQL.Remaining,
		GraphQLReset:     time.Unix(rl.Resources.GraphQL.Reset, 0).UTC(),
	}
}

type rateLimitFamily struct {
	Limit     int   `json:"limit"`
	Remaining int   `json:"remaining"`
	Reset     int64 `json:"reset"`
}
