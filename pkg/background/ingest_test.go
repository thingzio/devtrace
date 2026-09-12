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
	"sync/atomic"
	"testing"
	"time"
)

func TestStartIngestLoop(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	run := func(_ context.Context) error {
		calls.Add(1)
		return nil
	}

	cancel := StartIngestLoop(context.Background(), run, 50*time.Millisecond)
	defer cancel()

	// Wait long enough for at least the immediate call + 1 tick.
	time.Sleep(150 * time.Millisecond)

	got := calls.Load()
	if got < 2 {
		t.Errorf("expected at least 2 calls, got %d", got)
	}
}

func TestStartIngestLoopCancel(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	run := func(_ context.Context) error {
		calls.Add(1)
		return nil
	}

	cancel := StartIngestLoop(context.Background(), run, 50*time.Millisecond)

	// Let the immediate run happen.
	time.Sleep(30 * time.Millisecond)
	cancel()

	snapshot := calls.Load()

	// Wait to confirm no more calls after cancel.
	time.Sleep(100 * time.Millisecond)

	if calls.Load() != snapshot {
		t.Errorf("calls continued after cancel: before=%d, after=%d", snapshot, calls.Load())
	}
}
