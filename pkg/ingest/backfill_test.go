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

package ingest

import (
	"context"
	"testing"
	"time"
)

func TestBackfillHoursFullRange(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	hours := backfillHours(now, 3, time.Time{})

	if len(hours) != 72 {
		t.Fatalf("expected 72 hours, got %d", len(hours))
	}

	wantFirst := now.Add(-time.Hour) // most recent: 2025-06-15 11:00
	if !hours[0].Equal(wantFirst) {
		t.Errorf("first hour = %v, want %v", hours[0], wantFirst)
	}

	wantLast := now.Add(-72 * time.Hour) // oldest: 2025-06-12 12:00
	if !hours[len(hours)-1].Equal(wantLast) {
		t.Errorf("last hour = %v, want %v", hours[len(hours)-1], wantLast)
	}

	// Verify reverse-chronological order.
	for i := 1; i < len(hours); i++ {
		if !hours[i].Before(hours[i-1]) {
			t.Errorf("hours[%d] (%v) should be before hours[%d] (%v)", i, hours[i], i-1, hours[i-1])
		}
	}
}

func TestBackfillHoursPartialResume(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	cursor := now.Add(-24 * time.Hour) // cursor at 1 day ago
	hours := backfillHours(now, 3, cursor)

	if len(hours) != 48 {
		t.Fatalf("expected 48 hours, got %d", len(hours))
	}

	wantFirst := cursor.Add(-time.Hour) // cursor - 1h
	if !hours[0].Equal(wantFirst) {
		t.Errorf("first hour = %v, want %v", hours[0], wantFirst)
	}

	wantLast := now.Add(-72 * time.Hour)
	if !hours[len(hours)-1].Equal(wantLast) {
		t.Errorf("last hour = %v, want %v", hours[len(hours)-1], wantLast)
	}
}

func TestBackfillHoursAlreadyComplete(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	cursor := now.Add(-72 * time.Hour) // cursor at target boundary
	hours := backfillHours(now, 3, cursor)

	if len(hours) != 0 {
		t.Fatalf("expected 0 hours when already complete, got %d", len(hours))
	}
}

func TestBackfillZeroDays(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if err := Backfill(ctx, nil, 0); err != nil {
		t.Fatalf("Backfill(days=0) returned error: %v", err)
	}
	if err := Backfill(ctx, nil, -1); err != nil {
		t.Fatalf("Backfill(days=-1) returned error: %v", err)
	}
}
