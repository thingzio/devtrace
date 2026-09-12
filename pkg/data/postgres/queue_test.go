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
)

func TestEnqueueAndDequeue(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "queue-test-user"
	const provider = "github"

	// Clean up from prior runs.
	_ = store.RemoveFromQueue(ctx, user, provider)

	// Enqueue at priority 2.
	if err := store.EnqueueForScoring(ctx, user, provider, 2); err != nil {
		t.Fatalf("enqueue P2: %v", err)
	}

	// Enqueue same user at priority 1 — should upgrade.
	if err := store.EnqueueForScoring(ctx, user, provider, 1); err != nil {
		t.Fatalf("enqueue P1: %v", err)
	}

	entries, err := store.DequeueForScoring(ctx, 10)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.Username == user && e.Provider == provider {
			found = true
			if e.Priority != 1 {
				t.Errorf("expected priority 1, got %d", e.Priority)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected user in queue")
	}

	// Clean up.
	_ = store.RemoveFromQueue(ctx, user, provider)
}

func TestRemoveFromQueue(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "queue-rm-user"
	const provider = "github"

	// Clean up from prior runs.
	_ = store.RemoveFromQueue(ctx, user, provider)

	if err := store.EnqueueForScoring(ctx, user, provider, 2); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := store.RemoveFromQueue(ctx, user, provider); err != nil {
		t.Fatalf("remove: %v", err)
	}

	entries, err := store.DequeueForScoring(ctx, 100)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	for _, e := range entries {
		if e.Username == user && e.Provider == provider {
			t.Fatal("expected user removed from queue")
		}
	}
}

func TestContributorExists(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "exists-test-user"
	const provider = "github"

	// Clean up from prior runs.
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_reputation WHERE username = $1 AND provider = $2`, user, provider)
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_contributor WHERE username = $1 AND provider = $2`, user, provider)

	exists, err := store.ContributorExists(ctx, user, provider)
	if err != nil {
		t.Fatalf("exists (missing): %v", err)
	}
	if exists {
		t.Fatal("expected false for missing contributor")
	}

	// Insert contributor + reputation row.
	_, err = store.DB().ExecContext(ctx,
		`INSERT INTO devtrace_contributor (username, provider) VALUES ($1, $2)`, user, provider)
	if err != nil {
		t.Fatalf("insert contributor: %v", err)
	}
	_, err = store.DB().ExecContext(ctx,
		`INSERT INTO devtrace_reputation (username, provider, score, grade, model_version)
		 VALUES ($1, $2, 0.5, 'C', 'test')`, user, provider)
	if err != nil {
		t.Fatalf("insert reputation: %v", err)
	}

	exists, err = store.ContributorExists(ctx, user, provider)
	if err != nil {
		t.Fatalf("exists (present): %v", err)
	}
	if !exists {
		t.Fatal("expected true for existing contributor")
	}

	// Clean up.
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_reputation WHERE username = $1 AND provider = $2`, user, provider)
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_contributor WHERE username = $1 AND provider = $2`, user, provider)
}

func TestQueueDepth(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Baseline depth (may include rows from other tests).
	baseline, err := store.QueueDepth(ctx)
	if err != nil {
		t.Fatalf("queue depth (baseline): %v", err)
	}
	if baseline < 0 {
		t.Fatalf("expected non-negative depth, got %d", baseline)
	}

	// Enqueue one entry and verify depth increases.
	const user = "qdepth-test-user"
	const provider = "github"
	_ = store.RemoveFromQueue(ctx, user, provider)

	if enqErr := store.EnqueueForScoring(ctx, user, provider, 3); enqErr != nil {
		t.Fatalf("enqueue: %v", enqErr)
	}

	after, err := store.QueueDepth(ctx)
	if err != nil {
		t.Fatalf("queue depth (after enqueue): %v", err)
	}
	if after < baseline+1 {
		t.Errorf("expected depth >= %d, got %d", baseline+1, after)
	}

	// Clean up.
	_ = store.RemoveFromQueue(ctx, user, provider)
}

func TestGetTenantRepos(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Returns empty map when no installations exist (OK for local dev).
	repos, err := store.GetTenantRepos(ctx)
	if err != nil {
		t.Fatalf("get tenant repos: %v", err)
	}
	if repos == nil {
		t.Fatal("expected non-nil map")
	}
	t.Logf("got %d tenant repos", len(repos))
}
