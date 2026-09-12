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

func TestSecurityCreditsRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "ghsa-test-user"
	const provider = "github"
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_security_credit WHERE username = $1`, user)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_security_credit WHERE username = $1`, user)

	// Empty case.
	got, ts, err := store.GetSecurityCredits(ctx, user, provider)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if got != nil || !ts.IsZero() {
		t.Fatalf("empty: expected nil/zero, got %+v / %v", got, ts)
	}

	// Save a mix of credit types and severities.
	creds := []model.SecurityCredit{
		{
			AdvisoryID: "GHSA-aaaa-bbbb-cccc",
			CreditType: "reporter", Severity: "critical",
			CVEID: "CVE-2024-001", Summary: "Critical RCE",
			PublishedAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			AdvisoryID: "GHSA-dddd-eeee-ffff",
			CreditType: "reporter", Severity: "high",
			CVEID:       "CVE-2024-002",
			PublishedAt: time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			AdvisoryID: "GHSA-1111-2222-3333",
			CreditType: "fixer", Severity: "moderate",
			PublishedAt: time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			AdvisoryID: "GHSA-4444-5555-6666",
			CreditType: "analyst", Severity: "low",
			PublishedAt: time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	if serr := store.SaveSecurityCredits(ctx, user, provider, creds); serr != nil {
		t.Fatalf("save: %v", serr)
	}

	got, ts, err = store.GetSecurityCredits(ctx, user, provider)
	if err != nil {
		t.Fatalf("get after save: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil credits after save")
	}
	if got.ReporterCount != 2 {
		t.Errorf("reporter_count: got %d, want 2", got.ReporterCount)
	}
	if got.FixerCount != 1 {
		t.Errorf("fixer_count: got %d, want 1", got.FixerCount)
	}
	if got.OtherCount != 1 {
		t.Errorf("other_count: got %d, want 1 (analyst)", got.OtherCount)
	}
	if got.BySeverity["critical"] != 1 {
		t.Errorf("by_severity[critical]: got %d, want 1", got.BySeverity["critical"])
	}
	if got.BySeverity["high"] != 1 || got.BySeverity["moderate"] != 1 || got.BySeverity["low"] != 1 {
		t.Errorf("by_severity: got %+v", got.BySeverity)
	}
	if len(got.Recent) != 4 {
		t.Errorf("recent: got %d, want 4", len(got.Recent))
	}
	// Most recent first.
	if got.Recent[0].AdvisoryID != "GHSA-4444-5555-6666" {
		t.Errorf("recent[0]: got %s, want most-recent first", got.Recent[0].AdvisoryID)
	}
	if ts.IsZero() || time.Since(ts) > time.Minute {
		t.Errorf("fetched_at: got %v, expected ~now", ts)
	}
}

func TestSecurityCreditsSentinelOnEmpty(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "ghsa-empty-test"
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_security_credit WHERE username = $1`, user)
	})

	// Saving an empty slice should record the fetch attempt with a
	// sentinel row. Aggregate stays nil (no credits to display) but
	// fetched_at is non-zero so the service-layer TTL check suppresses
	// repeated re-fetching for users with no credits.
	if err := store.SaveSecurityCredits(ctx, user, "github", nil); err != nil {
		t.Fatalf("save empty: %v", err)
	}

	got, ts, err := store.GetSecurityCredits(ctx, user, "github")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil aggregate (sentinel filtered out), got %+v", got)
	}
	if ts.IsZero() {
		t.Error("fetched_at should be non-zero for sentinel row, suppressing re-fetch")
	}

	// But the sentinel row exists and would suppress re-fetch via a
	// direct query; verify by checking the table directly.
	var rowCount int
	if err := store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devtrace_security_credit WHERE username = $1`, user).Scan(&rowCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("expected 1 sentinel row, got %d", rowCount)
	}
}

func TestSecurityCreditsReplaceOnSave(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "ghsa-replace-test"
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_security_credit WHERE username = $1`, user)
	})

	first := []model.SecurityCredit{
		{AdvisoryID: "GHSA-old-old-1234", CreditType: "reporter", Severity: "high"},
	}
	if err := store.SaveSecurityCredits(ctx, user, "github", first); err != nil {
		t.Fatalf("first save: %v", err)
	}

	second := []model.SecurityCredit{
		{AdvisoryID: "GHSA-new-new-5678", CreditType: "fixer", Severity: "critical"},
	}
	if err := store.SaveSecurityCredits(ctx, user, "github", second); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, _, err := store.GetSecurityCredits(ctx, user, "github")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil after replace")
	}
	if got.ReporterCount != 0 || got.FixerCount != 1 {
		t.Errorf("counts after replace: reporter=%d fixer=%d, want 0/1",
			got.ReporterCount, got.FixerCount)
	}
	if len(got.Recent) != 1 || got.Recent[0].AdvisoryID != "GHSA-new-new-5678" {
		t.Errorf("expected only the new advisory, got %+v", got.Recent)
	}
}
