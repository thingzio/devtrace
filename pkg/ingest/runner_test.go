package ingest

import (
	"testing"
	"time"
)

func TestComputeHoursNoCursor(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Hour)
	lastAvailable := now.Add(-time.Hour)

	hours := computeHours(time.Time{}, 3, 24)

	if len(hours) != 3 {
		t.Fatalf("expected 3 hours, got %d", len(hours))
	}
	if !hours[len(hours)-1].Equal(lastAvailable) {
		t.Errorf("last hour = %v, want %v", hours[len(hours)-1], lastAvailable)
	}
	// Verify contiguous ascending order.
	for i := 1; i < len(hours); i++ {
		if !hours[i].Equal(hours[i-1].Add(time.Hour)) {
			t.Errorf("hours[%d]=%v not 1h after hours[%d]=%v", i, hours[i], i-1, hours[i-1])
		}
	}
}

func TestComputeHoursWithCursor(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Hour)
	lastAvailable := now.Add(-time.Hour)
	cursor := lastAvailable.Add(-2 * time.Hour) // 2 hours behind

	hours := computeHours(cursor, 1, 24)

	if len(hours) != 2 {
		t.Fatalf("expected 2 hours, got %d", len(hours))
	}
	if !hours[0].Equal(cursor.Add(time.Hour)) {
		t.Errorf("first hour = %v, want %v", hours[0], cursor.Add(time.Hour))
	}
	if !hours[len(hours)-1].Equal(lastAvailable) {
		t.Errorf("last hour = %v, want %v", hours[len(hours)-1], lastAvailable)
	}
}

func TestComputeHoursCaughtUp(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Hour)
	lastAvailable := now.Add(-time.Hour)

	hours := computeHours(lastAvailable, 5, 24)

	if len(hours) != 0 {
		t.Fatalf("expected 0 hours when caught up, got %d", len(hours))
	}
}

func TestComputeHoursCatchupMaxCap(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Hour)
	cursor := now.Add(-100 * time.Hour) // way behind

	hours := computeHours(cursor, 1, 5)

	if len(hours) != 5 {
		t.Fatalf("expected 5 hours (capped by catchupMax), got %d", len(hours))
	}
	// First hour should be cursor+1h.
	if !hours[0].Equal(cursor.Add(time.Hour)) {
		t.Errorf("first hour = %v, want %v", hours[0], cursor.Add(time.Hour))
	}
}

func TestComputeHoursLookbackNoCursor(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Hour)
	lastAvailable := now.Add(-time.Hour)

	// Fresh install with lookback=24, catchupMax=5.
	// Lookback should apply (not catchupMax) since there's no cursor.
	hours := computeHours(time.Time{}, 24, 5)

	if len(hours) != 24 {
		t.Fatalf("expected 24 hours (lookback for fresh install), got %d", len(hours))
	}
	if !hours[len(hours)-1].Equal(lastAvailable) {
		t.Errorf("last hour = %v, want %v", hours[len(hours)-1], lastAvailable)
	}
}
