package background

import (
	"context"
	"log/slog"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	ghclient "github.com/thingzio/devtrace/pkg/github"
)

const (
	quotaSampleInterval      = 10 * time.Minute
	quotaSampleRetentionDays = 30
)

// StartQuotaSampler periodically records token rate-limit snapshots.
// Returns a cancel function to stop the sampler.
func StartQuotaSampler(ctx context.Context, store *postgres.Store, pool *ghclient.TokenPool) func() {
	if pool == nil || store == nil {
		return func() {}
	}

	slog.Info("starting quota sampler", "interval", quotaSampleInterval)

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		sampleQuotas(ctx, store, pool)

		ticker := time.NewTicker(quotaSampleInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("quota sampler stopped")
				return
			case <-ticker.C:
				sampleQuotas(ctx, store, pool)
			}
		}
	}()

	return cancel
}

func sampleQuotas(ctx context.Context, store *postgres.Store, pool *ghclient.TokenPool) {
	quotas := pool.CheckQuotas(ctx)
	var sampled int
	for _, q := range quotas {
		if q.Error != "" {
			continue
		}
		used := q.Limit - q.Remaining
		if err := store.RecordTokenQuotaSample(ctx, q.InstallationID, q.Label, q.Limit, used); err != nil {
			slog.Warn("recording quota sample", "label", q.Label, "error", err)
			continue
		}
		sampled++
	}
	if sampled > 0 {
		slog.Debug("token quota samples recorded", "count", sampled)
	}

	// Purge old samples.
	threshold := time.Now().UTC().AddDate(0, 0, -quotaSampleRetentionDays)
	if purged, err := store.PurgeTokenQuotaSamples(ctx, threshold); err != nil {
		slog.Warn("purging old quota samples", "error", err)
	} else if purged > 0 {
		slog.Info("purged old quota samples", "count", purged)
	}
}
