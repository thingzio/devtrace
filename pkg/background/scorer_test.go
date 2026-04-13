package background

import (
	"testing"
)

func TestScorerConstants(t *testing.T) {
	t.Parallel()

	if defaultScorerSec != 3600 {
		t.Errorf("defaultScorerSec: got %d, want 3600", defaultScorerSec)
	}
	if scorerBatchSize != 100 {
		t.Errorf("scorerBatchSize: got %d, want 100", scorerBatchSize)
	}
	if defaultLowDays != 7 {
		t.Errorf("defaultLowDays: got %d, want 7", defaultLowDays)
	}
	if defaultHighDays != 30 {
		t.Errorf("defaultHighDays: got %d, want 30", defaultHighDays)
	}
}
