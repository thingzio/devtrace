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

func TestCrossVCSRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "cross-vcs-rt-test"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_cross_vcs WHERE provider=$1 AND username=$2`, provider, user)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_cross_vcs WHERE provider=$1 AND username=$2`, provider, user)

	got, ts, err := store.GetCrossVCS(ctx, provider, user)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if got != nil || !ts.IsZero() {
		t.Fatalf("empty: expected nil/zero, got %+v / %v", got, ts)
	}

	summary := &model.CrossVCS{
		TotalMatched: 2,
		Matches: []model.ForgeMatch{
			{Forge: "gitlab", URL: "https://gitlab.com/jane", KeyCount: 3, MatchedKeys: 2},
			{Forge: "codeberg", URL: "https://codeberg.org/jane", KeyCount: 1, MatchedKeys: 1},
		},
	}
	if serr := store.SaveCrossVCS(ctx, provider, user, summary); serr != nil {
		t.Fatalf("save: %v", serr)
	}

	got, ts, err = store.GetCrossVCS(ctx, provider, user)
	if err != nil {
		t.Fatalf("get after save: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil after save")
	}
	if got.TotalMatched != 2 || len(got.Matches) != 2 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Matches[0].Forge != "gitlab" || got.Matches[0].MatchedKeys != 2 {
		t.Errorf("Matches[0]: %+v", got.Matches[0])
	}
	if ts.IsZero() || time.Since(ts) > time.Minute {
		t.Errorf("fetched_at: got %v", ts)
	}
}

func TestCrossVCSSentinelOnNoMatches(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "cross-vcs-sentinel-test"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_cross_vcs WHERE provider=$1 AND username=$2`, provider, user)
	})

	// Sentinel: zero matches. Records the lookup attempt so the
	// TTL check stops us from re-asking forges the user doesn't
	// use. UI render path treats len(Matches)==0 as no cross-VCS data.
	if serr := store.SaveCrossVCS(ctx, provider, user, &model.CrossVCS{}); serr != nil {
		t.Fatalf("save sentinel: %v", serr)
	}
	got, ts, err := store.GetCrossVCS(ctx, provider, user)
	if err != nil {
		t.Fatalf("get sentinel: %v", err)
	}
	if got == nil {
		t.Fatal("sentinel should round-trip non-nil")
	}
	if got.TotalMatched != 0 || len(got.Matches) != 0 {
		t.Errorf("sentinel shape: %+v", got)
	}
	if ts.IsZero() {
		t.Error("fetched_at must be non-zero on sentinel")
	}
}

func TestCrossVCSUpsertReplaces(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "cross-vcs-replace-test"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_cross_vcs WHERE provider=$1 AND username=$2`, provider, user)
	})

	first := &model.CrossVCS{
		TotalMatched: 1,
		Matches:      []model.ForgeMatch{{Forge: "gitlab", MatchedKeys: 1}},
	}
	if err := store.SaveCrossVCS(ctx, provider, user, first); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second := &model.CrossVCS{
		TotalMatched: 2,
		Matches: []model.ForgeMatch{
			{Forge: "codeberg", MatchedKeys: 1},
			{Forge: "sourcehut", MatchedKeys: 1},
		},
	}
	if err := store.SaveCrossVCS(ctx, provider, user, second); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, _, err := store.GetCrossVCS(ctx, provider, user)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TotalMatched != 2 || len(got.Matches) != 2 || got.Matches[0].Forge != "codeberg" {
		t.Errorf("upsert should replace: %+v", got)
	}
}
