package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/model"
)

func TestRepoSummaryRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "repo-summary-test"
	const provider = "github"
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_repo_summary WHERE username = $1`, user)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_repo_summary WHERE username = $1`, user)

	// Empty case.
	got, ts, err := store.GetRepoSummary(ctx, user, provider)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if got != nil || !ts.IsZero() {
		t.Fatalf("empty: expected nil/zero, got %+v / %v", got, ts)
	}

	// Save and read back.
	summary := &model.OwnedRepos{
		TotalStars: 1234,
		TotalRepos: 8,
		Top: []model.OwnedRepo{
			{Name: "user/r1", Stars: 800, Language: "Go"},
			{Name: "user/r2", Stars: 300, Language: "Python"},
		},
		Languages: []model.LanguageBucket{
			{Language: "Go", Repos: 5, Share: 0.625},
			{Language: "Python", Repos: 3, Share: 0.375},
		},
	}
	if serr := store.SaveRepoSummary(ctx, user, provider, summary); serr != nil {
		t.Fatalf("save: %v", serr)
	}

	got, ts, err = store.GetRepoSummary(ctx, user, provider)
	if err != nil {
		t.Fatalf("get after save: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil after save")
	}
	if got.TotalStars != 1234 || got.TotalRepos != 8 {
		t.Errorf("totals: got %d stars / %d repos, want 1234 / 8", got.TotalStars, got.TotalRepos)
	}
	if len(got.Top) != 2 || got.Top[0].Name != "user/r1" {
		t.Errorf("top: got %+v", got.Top)
	}
	if len(got.Languages) != 2 || got.Languages[0].Language != "Go" {
		t.Errorf("languages: got %+v", got.Languages)
	}
	if ts.IsZero() || time.Since(ts) > time.Minute {
		t.Errorf("fetched_at: got %v, expected ~now", ts)
	}

	// Re-save with different content — verify upsert.
	updated := &model.OwnedRepos{TotalStars: 2000, TotalRepos: 10}
	if serr := store.SaveRepoSummary(ctx, user, provider, updated); serr != nil {
		t.Fatalf("re-save: %v", serr)
	}
	got, _, err = store.GetRepoSummary(ctx, user, provider)
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}
	if got.TotalStars != 2000 || got.TotalRepos != 10 {
		t.Errorf("upsert: got %d/%d, want 2000/10", got.TotalStars, got.TotalRepos)
	}
	if len(got.Top) != 0 || len(got.Languages) != 0 {
		t.Errorf("upsert should clear sub-fields; got top=%v langs=%v", got.Top, got.Languages)
	}
}

func TestRepoSummarySaveNil(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const user = "repo-summary-nil-test"
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_repo_summary WHERE username = $1`, user)
	})

	// Saving nil should still record a row so the refresh worker doesn't
	// repeatedly retry a no-data contributor.
	if err := store.SaveRepoSummary(ctx, user, "github", nil); err != nil {
		t.Fatalf("save nil: %v", err)
	}
	got, ts, err := store.GetRepoSummary(ctx, user, "github")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected zero-value summary record")
	}
	if got.TotalStars != 0 || got.TotalRepos != 0 || len(got.Top) != 0 || len(got.Languages) != 0 {
		t.Errorf("expected all-zero summary, got %+v", got)
	}
	if ts.IsZero() {
		t.Error("fetched_at should be non-zero")
	}
}
