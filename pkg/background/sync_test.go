package background

import (
	"testing"
)

func TestSyncConstants(t *testing.T) {
	t.Parallel()

	if defaultSyncSec != 1800 {
		t.Errorf("defaultSyncSec: got %d, want 1800", defaultSyncSec)
	}
	if syncBatchSize != 500 {
		t.Errorf("syncBatchSize: got %d, want 500", syncBatchSize)
	}
	if syncStateKey != "devpulse_sync" {
		t.Errorf("syncStateKey: got %q, want %q", syncStateKey, "devpulse_sync")
	}
}
