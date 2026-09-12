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
