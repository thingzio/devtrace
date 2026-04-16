# Dynamic Token Pool Design

## Problem

The GitHub token pool is built once at startup by minting installation tokens for all active installations. Webhooks save new installations to DB but the pool is never refreshed — requires a restart. Installation tokens expire after 1 hour, so the pool runs on borrowed time.

## Solution

Replace the static `[]string` token pool with installation-aware entries that track expiry. A single background goroutine owns all pool mutations — triggered by either a periodic timer (checks every 1 minute, only mints when tokens near expiry) or a channel signal from webhooks.

## Data Model

### poolEntry (internal)

```go
type poolEntry struct {
    installationID int64     // 0 for PAT tokens
    label          string    // target login (org/user) or "PAT"
    token          string
    expiresAt      time.Time // zero for PATs (never expire)
}
```

### TokenQuota (add Label field)

```go
type TokenQuota struct {
    Index     int
    Label     string // target login or "PAT"
    Limit     int
    Remaining int
    Reset     time.Time
    Error     string
}
```

## TokenPool Changes

- Internal storage: `[]poolEntry` instead of `[]string`
- `Token()`: skip entries where `time.Now()` is within 5 min of `expiresAt`, in addition to existing exhaustion skip
- `Replace(entries []poolEntry)`: atomic swap behind mutex, resets cursor/counts
- `CheckQuotas`: populates `Label` from entry
- PATs (zero `expiresAt`) never skipped for expiry

## Refresh Goroutine (pool_refresh.go)

```go
func StartPoolRefresh(ctx context.Context, pool *TokenPool,
    refreshFn func(ctx context.Context) ([]poolEntry, error),
    notifyCh <-chan struct{}) func()
```

- Select on: 1-minute ticker, `notifyCh`, `ctx.Done()`
- On tick: check if any entry expires within 5 min — if yes, call `refreshFn` + `pool.Replace`
- On notify: call `refreshFn` + `pool.Replace` immediately (30s dedup)
- On failure: log, keep existing tokens, retry next tick

## Webhook Handler Change

- Accept `notifyCh chan<- struct{}` parameter
- Non-blocking send on `created`, `deleted`, `suspend` actions

## Wiring (server.go)

- `buildGitHubClient` returns pool with `poolEntry` (including `expiresAt` from mint response)
- Create `notifyCh := make(chan struct{}, 1)`, pass to webhook handler and `StartPoolRefresh`
- `StartPoolRefresh` returns cancel func, deferred in `Run()`

## Token Lifecycle

```
mint -> cache (expiresAt from GitHub) -> serve ~55 min -> refresh replaces -> old token expires
```

## Edge Cases

- All mints fail: keep existing tokens until they expire, log error, retry next tick
- Empty pool after refresh: `Token()` returns "" (existing behavior)
- Webhook before timer: buffered(1) channel won't block
- Rapid webhooks: 30s dedup collapses into one refresh
- Token expires between checks: `Token()` skips expired entries

## Admin Display

`tokenQuotaRow` gets `Label string` (target login or "PAT") for stable identity instead of shifting index numbers.

## Tests

| Test | Verifies |
|---|---|
| `TestPoolEntryExpiry` | `Token()` skips entries within 5 min of expiry |
| `TestPoolReplace` | Atomic swap, cursor reset, old tokens gone |
| `TestPoolReplacePreservesExhaustion` | Exhausted state cleared on replace |
| `TestPoolMixedPATAndInstallation` | PATs never skipped for expiry |
| `TestRefreshOnTimer` | Goroutine refreshes when token nears expiry |
| `TestRefreshOnNotify` | Channel signal triggers immediate refresh |
| `TestRefreshDedup` | Rapid signals don't cause multiple refreshes within 30s |
| `TestRefreshFailureKeepsOldTokens` | Failed mint preserves existing pool |
| `TestRefreshAllMintsFail` | Pool retains old tokens, logs error |
| `TestRefreshEmptyDB` | No installations -> PAT-only pool |
| `TestTokenQuotaLabel` | CheckQuotas returns correct labels |
| `TestWebhookNotifyNonBlocking` | Webhook doesn't block if channel full |
