package background

import (
	"context"
	"testing"
	"time"

	ghclient "github.com/thingzio/devtrace/pkg/github"
)

func TestStartQuotaSampler_NilPool(t *testing.T) {
	t.Parallel()
	cancel := StartQuotaSampler(context.Background(), nil, nil)
	// Should not panic and return a no-op cancel.
	cancel()
}

func TestStartQuotaSampler_NilStore(t *testing.T) {
	t.Parallel()
	pool := ghclient.NewTokenPool("tok1")
	cancel := StartQuotaSampler(context.Background(), nil, pool)
	cancel()
}

func TestStartQuotaSampler_CancelStops(t *testing.T) {
	t.Parallel()
	// Use an empty pool so CheckQuotas returns no entries and no HTTP calls are made.
	pool := ghclient.NewTokenPool()
	cancel := StartQuotaSampler(context.Background(), nil, pool)
	// Cancel immediately — should not hang.
	cancel()
}

func TestQuotaSamplerConstants(t *testing.T) {
	t.Parallel()
	if quotaSampleInterval != 10*time.Minute {
		t.Errorf("quotaSampleInterval = %v, want 10m", quotaSampleInterval)
	}
	if quotaSampleRetentionDays != 30 {
		t.Errorf("quotaSampleRetentionDays = %d, want 30", quotaSampleRetentionDays)
	}
}
