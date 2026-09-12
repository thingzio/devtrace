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

	"github.com/thingzio/devtrace/pkg/config"
)

const defaultIngestSec = 600 // 10 minutes

// IngestFunc is the function signature for a single ingest run.
type IngestFunc func(ctx context.Context) error

// StartIngestLoop runs the given function immediately, then on a ticker interval.
// If interval is 0, it reads INGEST_INTERVAL_SEC (default 600s).
// Returns a cancel function to stop the loop.
func StartIngestLoop(ctx context.Context, run IngestFunc, interval time.Duration) func() {
	if interval == 0 {
		interval = time.Duration(config.GetEnvAsInt("INGEST_INTERVAL_SEC", defaultIngestSec)) * time.Second
	}
	slog.Info("starting ingest loop", "interval", interval)

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		// Run immediately on startup.
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
