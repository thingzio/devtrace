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

func TestOSSFScorecardRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		owner    = "ossf-test-owner"
		repo     = "ossf-test-repo"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_ossf_scorecard
			 WHERE provider=$1 AND repo_owner=$2 AND repo_name=$3`,
			provider, owner, repo)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_ossf_scorecard
		 WHERE provider=$1 AND repo_owner=$2 AND repo_name=$3`,
		provider, owner, repo)

	got, ts, err := store.GetOSSFScorecard(ctx, provider, owner, repo)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if got != nil || !ts.IsZero() {
		t.Fatalf("empty: expected nil/zero, got %+v / %v", got, ts)
	}

	card := &model.OSSFScorecard{
		Score:        7.5,
		Date:         time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC),
		Commit:       "abc123",
		ScorecardVer: "v5.0.0",
		Checks: []model.OSSFCheck{
			{Name: "Code-Review", Score: 10, Reason: "all reviewed", DocURL: "https://x/cr"},
			{Name: "Fuzzing", Score: -1, Reason: "not fuzzed", DocURL: "https://x/fz"},
			{Name: "License", Score: 9},
		},
	}
	if serr := store.SaveOSSFScorecard(ctx, provider, owner, repo, card); serr != nil {
		t.Fatalf("save: %v", serr)
	}

	got, ts, err = store.GetOSSFScorecard(ctx, provider, owner, repo)
	if err != nil {
		t.Fatalf("get after save: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil after save")
	}
	if got.Score != 7.5 {
		t.Errorf("score: got %v, want 7.5", got.Score)
	}
	if got.Commit != "abc123" {
		t.Errorf("commit: got %q", got.Commit)
	}
	if got.ScorecardVer != "v5.0.0" {
		t.Errorf("scorecard_version: got %q", got.ScorecardVer)
	}
	if !got.Date.Equal(card.Date) {
		t.Errorf("date: got %v, want %v", got.Date, card.Date)
	}
	if len(got.Checks) != 3 {
		t.Fatalf("checks: got %d, want 3", len(got.Checks))
	}
	if got.Checks[1].Score != -1 {
		t.Errorf("checks[1] not-applicable preserved: got %d", got.Checks[1].Score)
	}
	if ts.IsZero() || time.Since(ts) > time.Minute {
		t.Errorf("fetched_at: got %v", ts)
	}
}

func TestOSSFScorecardSentinelOnEmpty(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		owner    = "ossf-empty-owner"
		repo     = "ossf-empty-repo"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_ossf_scorecard
			 WHERE provider=$1 AND repo_owner=$2 AND repo_name=$3`,
			provider, owner, repo)
	})

	// Sentinel: zero-score, no checks. Records the fetch attempt so the
	// service layer's TTL check suppresses repeated calls to OSSF for
	// repos it has no scorecard for.
	if err := store.SaveOSSFScorecard(ctx, provider, owner, repo,
		&model.OSSFScorecard{}); err != nil {
		t.Fatalf("save sentinel: %v", err)
	}

	got, ts, err := store.GetOSSFScorecard(ctx, provider, owner, repo)
	if err != nil {
		t.Fatalf("get sentinel: %v", err)
	}
	if got == nil {
		t.Fatal("sentinel should return a non-nil card")
	}
	if got.Score != 0 || len(got.Checks) != 0 {
		t.Errorf("sentinel shape: got score=%v checks=%d", got.Score, len(got.Checks))
	}
	if ts.IsZero() {
		t.Error("fetched_at must be non-zero on sentinel")
	}
}

func TestOSSFScorecardUpsertReplaces(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		owner    = "ossf-replace-owner"
		repo     = "ossf-replace-repo"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_ossf_scorecard
			 WHERE provider=$1 AND repo_owner=$2 AND repo_name=$3`,
			provider, owner, repo)
	})

	first := &model.OSSFScorecard{
		Score:  4.0,
		Checks: []model.OSSFCheck{{Name: "License", Score: 0}},
	}
	if err := store.SaveOSSFScorecard(ctx, provider, owner, repo, first); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second := &model.OSSFScorecard{
		Score:  9.5,
		Checks: []model.OSSFCheck{{Name: "License", Score: 10}, {Name: "Maintained", Score: 8}},
	}
	if err := store.SaveOSSFScorecard(ctx, provider, owner, repo, second); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, _, err := store.GetOSSFScorecard(ctx, provider, owner, repo)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Score != 9.5 {
		t.Errorf("score: got %v, want 9.5 (replace not append)", got.Score)
	}
	if len(got.Checks) != 2 {
		t.Errorf("checks count: got %d, want 2", len(got.Checks))
	}
}
