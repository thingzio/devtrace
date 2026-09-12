// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
		coreUsed := q.Limit - q.Remaining
		searchUsed := q.SearchLimit - q.SearchRemaining
		graphqlUsed := q.GraphQLLimit - q.GraphQLRemaining
		if err := store.RecordTokenQuotaSample(ctx, q.InstallationID, q.Label,
			q.Limit, coreUsed,
			q.SearchLimit, searchUsed,
			q.GraphQLLimit, graphqlUsed,
		); err != nil {
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
