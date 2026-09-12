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

package postgres_test

import (
	"context"
	"testing"
	"time"
)

func TestSyncState(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const key = "test-sync-cursor"

	// Clean up any leftover state from prior runs.
	_, _ = store.DB().ExecContext(ctx, `DELETE FROM devtrace_sync_state WHERE key = $1`, key)

	// Should return zero time when key does not exist.
	got, err := store.GetSyncState(ctx, key)
	if err != nil {
		t.Fatalf("get (missing): %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("expected zero time, got %v", got)
	}

	// Save and retrieve.
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	if err = store.SaveSyncState(ctx, key, ts); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err = store.GetSyncState(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Equal(ts) {
		t.Fatalf("round-trip: got %v, want %v", got, ts)
	}

	// Overwrite and verify.
	ts2 := time.Date(2025, 7, 1, 8, 30, 0, 0, time.UTC)
	if err = store.SaveSyncState(ctx, key, ts2); err != nil {
		t.Fatalf("save (overwrite): %v", err)
	}
	got, err = store.GetSyncState(ctx, key)
	if err != nil {
		t.Fatalf("get (overwrite): %v", err)
	}
	if !got.Equal(ts2) {
		t.Fatalf("overwrite: got %v, want %v", got, ts2)
	}
}
