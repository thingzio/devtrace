package profile_test

import (
	"math"
	"testing"

	ghclient "github.com/thingzio/devtrace/pkg/github"
	"github.com/thingzio/devtrace/pkg/profile"
)

func TestAggregateReposNilOrEmpty(t *testing.T) {
	t.Parallel()

	if got := profile.AggregateRepos(nil); got != nil {
		t.Errorf("nil input: got %+v, want nil", got)
	}
	// Forks-only input should also yield nil — no real owned work.
	forksOnly := []ghclient.Repo{
		{Name: "a", FullName: "u/a", Fork: true, Stars: 100},
		{Name: "b", FullName: "u/b", Fork: true, Stars: 50},
	}
	if got := profile.AggregateRepos(forksOnly); got != nil {
		t.Errorf("forks-only: got %+v, want nil", got)
	}
}

func TestAggregateReposExcludesForksAndArchived(t *testing.T) {
	t.Parallel()

	repos := []ghclient.Repo{
		{Name: "live", FullName: "u/live", Stars: 50, Language: "Go"},
		{Name: "fork", FullName: "u/fork", Stars: 999, Language: "Go", Fork: true},
		{Name: "old", FullName: "u/old", Stars: 200, Language: "Python", Archived: true},
		{Name: "new", FullName: "u/new", Stars: 5, Language: "Go"},
	}
	got := profile.AggregateRepos(repos)
	if got == nil {
		t.Fatal("expected non-nil aggregate")
	}
	if got.TotalRepos != 2 {
		t.Errorf("TotalRepos: got %d, want 2", got.TotalRepos)
	}
	if got.TotalStars != 55 {
		t.Errorf("TotalStars: got %d, want 55 (50+5, fork and archived excluded)", got.TotalStars)
	}
	for _, top := range got.Top {
		if top.Name == "u/fork" || top.Name == "u/old" {
			t.Errorf("top should not contain fork/archived: %s", top.Name)
		}
	}
}

func TestAggregateReposTopByStars(t *testing.T) {
	t.Parallel()

	repos := make([]ghclient.Repo, 0, 8)
	repos = append(repos,
		ghclient.Repo{FullName: "u/r1", Stars: 100, Language: "Go"},
		ghclient.Repo{FullName: "u/r2", Stars: 50, Language: "Go"},
		ghclient.Repo{FullName: "u/r3", Stars: 25, Language: "Python"},
		ghclient.Repo{FullName: "u/r4", Stars: 10, Language: "Python"},
		ghclient.Repo{FullName: "u/r5", Stars: 5, Language: "Rust"},
		ghclient.Repo{FullName: "u/r6", Stars: 3, Language: "Rust"},
		ghclient.Repo{FullName: "u/r7", Stars: 0, Language: "Go"}, // excluded by minStars
	)
	got := profile.AggregateRepos(repos)
	if got == nil {
		t.Fatal("expected aggregate")
	}
	if len(got.Top) != 5 {
		t.Fatalf("Top length: got %d, want 5 (limit) — got: %+v", len(got.Top), got.Top)
	}
	expectedOrder := []string{"u/r1", "u/r2", "u/r3", "u/r4", "u/r5"}
	for i, want := range expectedOrder {
		if got.Top[i].Name != want {
			t.Errorf("Top[%d]: got %s, want %s", i, got.Top[i].Name, want)
		}
	}
}

func TestAggregateReposTopByStarsTieBreak(t *testing.T) {
	t.Parallel()

	// Identical stars — tie-break by full_name ascending.
	repos := []ghclient.Repo{
		{FullName: "u/zzz", Stars: 10, Language: "Go"},
		{FullName: "u/aaa", Stars: 10, Language: "Go"},
		{FullName: "u/mmm", Stars: 10, Language: "Go"},
	}
	got := profile.AggregateRepos(repos)
	if got.Top[0].Name != "u/aaa" || got.Top[1].Name != "u/mmm" || got.Top[2].Name != "u/zzz" {
		t.Errorf("tie-break order: %+v", []string{got.Top[0].Name, got.Top[1].Name, got.Top[2].Name})
	}
}

func TestAggregateReposLanguages(t *testing.T) {
	t.Parallel()

	repos := []ghclient.Repo{
		{FullName: "u/g1", Stars: 1, Language: "Go"},
		{FullName: "u/g2", Stars: 1, Language: "Go"},
		{FullName: "u/g3", Stars: 1, Language: "Go"},
		{FullName: "u/p1", Stars: 1, Language: "Python"},
		{FullName: "u/p2", Stars: 1, Language: "Python"},
		{FullName: "u/r1", Stars: 1, Language: "Rust"},
		{FullName: "u/empty", Stars: 1, Language: ""}, // unclassified, excluded from share denominator
	}
	got := profile.AggregateRepos(repos)
	if got == nil {
		t.Fatal("expected aggregate")
	}
	if len(got.Languages) != 3 {
		t.Fatalf("Languages: got %d, want 3", len(got.Languages))
	}
	if got.Languages[0].Language != "Go" || got.Languages[0].Repos != 3 {
		t.Errorf("top language: got %+v, want Go/3", got.Languages[0])
	}
	// Share denominator excludes unclassified repos: 3+2+1 = 6.
	if !floatNear(got.Languages[0].Share, 0.5) {
		t.Errorf("Go share: got %f, want 0.5", got.Languages[0].Share)
	}
	if !floatNear(got.Languages[1].Share, 2.0/6.0) {
		t.Errorf("Python share: got %f, want ~0.333", got.Languages[1].Share)
	}
}

func TestAggregateReposLanguagesUnclassified(t *testing.T) {
	t.Parallel()

	repos := []ghclient.Repo{
		{FullName: "u/a", Stars: 1, Language: ""},
		{FullName: "u/b", Stars: 1, Language: ""},
	}
	got := profile.AggregateRepos(repos)
	if got == nil {
		t.Fatal("expected aggregate")
	}
	if len(got.Languages) != 0 {
		t.Errorf("all-unclassified should produce no language buckets, got %+v", got.Languages)
	}
}

func floatNear(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}
