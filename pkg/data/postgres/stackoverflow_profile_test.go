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

	"github.com/thingzio/devtrace/pkg/model"
)

func TestStackOverflowProfileRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "so-rt-test-user"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_stackoverflow_profile WHERE provider=$1 AND username=$2`, provider, user)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_stackoverflow_profile WHERE provider=$1 AND username=$2`, provider, user)

	got, ts, err := store.GetStackOverflowProfile(ctx, provider, user)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if got != nil || !ts.IsZero() {
		t.Fatalf("empty: expected nil/zero, got %+v / %v", got, ts)
	}

	created := time.Date(2008, 9, 26, 12, 0, 0, 0, time.UTC)
	lastAccess := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	profile := &model.StackOverflow{
		UserID:       22656,
		DisplayName:  "Jon Skeet",
		Reputation:   1500000,
		BadgeBronze:  9000,
		BadgeSilver:  9000,
		BadgeGold:    800,
		URL:          "https://stackoverflow.com/users/22656/jon-skeet",
		CreatedAt:    &created,
		LastAccessAt: &lastAccess,
	}
	if serr := store.SaveStackOverflowProfile(ctx, provider, user, profile); serr != nil {
		t.Fatalf("save: %v", serr)
	}

	got, ts, err = store.GetStackOverflowProfile(ctx, provider, user)
	if err != nil {
		t.Fatalf("get after save: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil after save")
	}
	if got.UserID != 22656 || got.Reputation != 1500000 || got.BadgeGold != 800 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.CreatedAt == nil || !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt: got %v, want %v", got.CreatedAt, created)
	}
	if ts.IsZero() || time.Since(ts) > time.Minute {
		t.Errorf("fetched_at: got %v", ts)
	}
}

func TestStackOverflowSentinelOnNoLink(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "so-no-link-test"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_stackoverflow_profile WHERE provider=$1 AND username=$2`, provider, user)
	})

	// Sentinel: zero user_id and zero reputation. Records the lookup
	// attempt so the TTL check suppresses re-asking. UI render path
	// treats user_id==0 as "no SO data" and omits the section.
	if err := store.SaveStackOverflowProfile(ctx, provider, user,
		&model.StackOverflow{}); err != nil {
		t.Fatalf("save sentinel: %v", err)
	}
	got, ts, err := store.GetStackOverflowProfile(ctx, provider, user)
	if err != nil {
		t.Fatalf("get sentinel: %v", err)
	}
	if got == nil {
		t.Fatal("sentinel should round-trip non-nil")
	}
	if got.UserID != 0 || got.Reputation != 0 {
		t.Errorf("sentinel shape: %+v", got)
	}
	if ts.IsZero() {
		t.Error("fetched_at must be non-zero on sentinel")
	}
}

func TestStackOverflowUpsertReplaces(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "so-replace-test"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_stackoverflow_profile WHERE provider=$1 AND username=$2`, provider, user)
	})

	first := &model.StackOverflow{UserID: 100, Reputation: 50, DisplayName: "old"}
	if err := store.SaveStackOverflowProfile(ctx, provider, user, first); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second := &model.StackOverflow{UserID: 100, Reputation: 75, DisplayName: "new", BadgeGold: 1}
	if err := store.SaveStackOverflowProfile(ctx, provider, user, second); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, _, err := store.GetStackOverflowProfile(ctx, provider, user)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Reputation != 75 || got.DisplayName != "new" || got.BadgeGold != 1 {
		t.Errorf("upsert should replace: %+v", got)
	}
}
