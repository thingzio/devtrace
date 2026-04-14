# Merged Service Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Merge the ingest job into the serve binary so all background work (ingest, scoring, sync) runs in a single Cloud Run service with min/max instances = 1.

**Architecture:** The existing `ingest.Run()` becomes a background goroutine loop in the serve binary, alongside the existing scorer and DevPulse sync. The scorer runs immediately on first tick (not delayed). Cloud Run job and its scheduler are removed.

**Tech Stack:** Go, Cloud Run, Cloud SQL (PostgreSQL), GH Archive

---

### Task 1: Add StartIngestLoop to background package

**Files:**
- Create: `pkg/background/ingest.go`
- Test: `pkg/background/ingest_test.go`

**Step 1: Write the test**

```go
// pkg/background/ingest_test.go
package background

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartIngestLoop_CallsRunOnTick(t *testing.T) {
	var called atomic.Int32
	runner := func(ctx context.Context) error {
		called.Add(1)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := StartIngestLoop(ctx, runner, 50*time.Millisecond)
	defer stop()

	// Wait for at least 2 ticks
	time.Sleep(150 * time.Millisecond)

	if got := called.Load(); got < 2 {
		t.Fatalf("expected >=2 calls, got %d", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestStartIngestLoop ./pkg/background/ -v`
Expected: FAIL — `StartIngestLoop` not defined

**Step 3: Write implementation**

```go
// pkg/background/ingest.go
package background

import (
	"context"
	"log/slog"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
)

const defaultIngestSec = 600 // 10 minutes

// IngestFunc is the signature for the ingest runner.
type IngestFunc func(ctx context.Context) error

// StartIngestLoop runs the ingest function on a recurring interval.
// Returns a cancel function to stop the loop.
func StartIngestLoop(ctx context.Context, run IngestFunc, interval time.Duration) func() {
	if interval == 0 {
		interval = time.Duration(config.GetEnvAsInt("INGEST_INTERVAL_SEC", defaultIngestSec)) * time.Second
	}
	slog.Info("starting ingest loop", "interval", interval)

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		// Run once immediately on startup.
		if err := run(ctx); err != nil {
			slog.Error("ingest run failed", "error", err)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("ingest loop stopped")
				return
			case <-ticker.C:
				if err := run(ctx); err != nil {
					slog.Error("ingest run failed", "error", err)
				}
			}
		}
	}()

	return cancel
}
```

**Step 4: Run test to verify it passes**

Run: `go test -run TestStartIngestLoop ./pkg/background/ -v`
Expected: PASS

**Step 5: Commit**

```
git add pkg/background/ingest.go pkg/background/ingest_test.go
git commit -S -m "Add StartIngestLoop background worker"
```

---

### Task 2: Make background scorer run immediately on first tick

**Files:**
- Modify: `pkg/background/scorer.go:40-54`

**Step 1: Update scorer to run immediately**

Change the goroutine in `StartBackgroundScorer` to run once before entering the ticker loop:

```go
go func() {
    // Run once immediately to drain any pending queue.
    runScorer(ctx, store, gh, version)

    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            slog.Info("background scorer stopped")
            return
        case <-ticker.C:
            runScorer(ctx, store, gh, version)
        }
    }
}()
```

**Step 2: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 3: Commit**

```
git add pkg/background/scorer.go
git commit -S -m "Run background scorer immediately on startup"
```

---

### Task 3: Wire ingest loop into server.go

**Files:**
- Modify: `pkg/server/server.go:107-113`

**Step 1: Add ingest loop start alongside existing background ops**

In the `ENABLE_BACKGROUND_OPS` block, add:

```go
if config.GetEnvBool("ENABLE_BACKGROUND_OPS") {
    syncStop := background.StartDevPulseSync(ctx, store)
    defer syncStop()

    scorerStop := background.StartBackgroundScorer(ctx, store, ghClient, opts.Version)
    defer scorerStop()

    ingestStop := background.StartIngestLoop(ctx, func(ctx context.Context) error {
        return ingest.Run(ctx, store)
    }, 0) // 0 = use INGEST_INTERVAL_SEC env or default 10min
    defer ingestStop()
}
```

Add import: `"github.com/thingzio/devtrace/pkg/ingest"`

**Step 2: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 3: Commit**

```
git add pkg/server/server.go
git commit -S -m "Wire ingest loop into serve binary"
```

---

### Task 4: Remove ingest binary and Cloud Run job references

**Files:**
- Delete: `cmd/devtrace-ingest/main.go`
- Modify: `.goreleaser.yaml:24-36` (remove ingest build)
- Modify: `.goreleaser.yaml:62-67` (remove ingest docker)
- Modify: `.github/workflows/deploy-cloud-run.yaml:43-45` (remove job update)

**Step 1: Delete ingest binary**

```
rm cmd/devtrace-ingest/main.go
rmdir cmd/devtrace-ingest
```

**Step 2: Remove ingest build from goreleaser**

Remove the `- id: devtrace-ingest` build block and the `- id: devtrace-ingest` docker block from `.goreleaser.yaml`.

**Step 3: Remove job deploy from workflow**

Remove the `gcloud run jobs update` lines from `.github/workflows/deploy-cloud-run.yaml`.

**Step 4: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 5: Commit**

```
git add -A
git commit -S -m "Remove standalone ingest binary and Cloud Run job config"
```

---

### Task 5: Update Cloud Run service config (min/max instances)

**Files:**
- Modify: `.github/workflows/deploy-cloud-run.yaml` or document manual step

**Step 1: Add min/max instance flags to deploy workflow**

Update the `gcloud run services update` command:

```yaml
gcloud run services update ${{ vars.SERVICE_NAME }} \
  --region=${{ vars.REGION }} \
  --image="${AR}/devtrace-site:${TAG}" \
  --min-instances=1 \
  --max-instances=1
```

**Step 2: Commit**

```
git add .github/workflows/deploy-cloud-run.yaml
git commit -S -m "Set Cloud Run min/max instances to 1"
```

---

### Task 6: Run full qualification

**Step 1: Run make qualify**

Run: `make qualify`
Expected: All tests pass, lint clean, no vulns

**Step 2: Fix any issues found**

**Step 3: Final commit if needed**

---

### Task 7: Manual cleanup (post-deploy)

These are manual steps after the new version is deployed:

1. Delete Cloud Scheduler trigger for ingest job
2. Delete Cloud Run job `devtrace-saas-ingest`
3. Verify background ops in logs: `gcloud logging read` should show "starting ingest loop", "starting background scorer", "starting devpulse sync"
4. Verify ingest runs: logs should show "processing archive" within 10 minutes of deploy
