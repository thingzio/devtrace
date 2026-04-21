package ingest

import (
	"testing"
	"time"
)

func TestAggregatorEmpty(t *testing.T) {
	a := NewAggregator(time.Now())
	if got := a.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
	if got := a.Results(); len(got) != 0 {
		t.Fatalf("Results len = %d, want 0", len(got))
	}
}

func TestAggregatorSingleUser(t *testing.T) {
	a := NewAggregator(time.Now())

	events := []Event{
		{Type: "PullRequestEvent", Action: "opened", Actor: "alice", Repo: "org/repo1"},
		{Type: "PullRequestEvent", Action: "opened", Actor: "alice", Repo: "org/repo1"},
		{Type: "PullRequestEvent", Action: "closed", Actor: "alice", Repo: "org/repo1"},
		{Type: "PullRequestReviewEvent", Action: "submitted", Actor: "alice", Repo: "org/repo2"},
		{Type: "IssueCommentEvent", Action: "created", Actor: "alice", Repo: "org/repo2"},
	}
	for _, ev := range events {
		a.Add(ev)
	}

	if got := a.Count(); got != 1 {
		t.Fatalf("Count = %d, want 1", got)
	}

	results := a.Results()
	if len(results) != 1 {
		t.Fatalf("Results len = %d, want 1", len(results))
	}

	s := results[0]
	if s.Username != "alice" {
		t.Errorf("Username = %q, want %q", s.Username, "alice")
	}
	if s.PRsOpened != 2 {
		t.Errorf("PRsOpened = %d, want 2", s.PRsOpened)
	}
	if s.PRsClosed != 1 {
		t.Errorf("PRsClosed = %d, want 1", s.PRsClosed)
	}
	if s.ReviewsGiven != 1 {
		t.Errorf("ReviewsGiven = %d, want 1", s.ReviewsGiven)
	}
	if s.IssueComments != 1 {
		t.Errorf("IssueComments = %d, want 1", s.IssueComments)
	}
}

func TestAggregatorMultipleUsers(t *testing.T) {
	a := NewAggregator(time.Now())

	users := []string{"alice", "bob", "carol"}
	for _, u := range users {
		a.Add(Event{Type: "PullRequestEvent", Action: "opened", Actor: u, Repo: "org/repo"})
	}

	if got := a.Count(); got != 3 {
		t.Fatalf("Count = %d, want 3", got)
	}
	if got := len(a.Results()); got != 3 {
		t.Fatalf("Results len = %d, want 3", got)
	}
}

func TestAggregatorRepoDedup(t *testing.T) {
	a := NewAggregator(time.Now())

	// 5 events, same repo
	for i := 0; i < 5; i++ {
		a.Add(Event{Type: "PullRequestEvent", Action: "opened", Actor: "alice", Repo: "org/repo1"})
	}
	results := a.Results()
	if len(results) != 1 {
		t.Fatalf("Results len = %d, want 1", len(results))
	}
	if got := len(results[0].Repos); got != 1 {
		t.Errorf("Repos count = %d, want 1 (dedup)", got)
	}

	// New aggregator: 3 different repos
	a2 := NewAggregator(time.Now())
	repos := []string{"org/repo1", "org/repo2", "org/repo3"}
	for _, r := range repos {
		a2.Add(Event{Type: "IssueCommentEvent", Action: "created", Actor: "bob", Repo: r})
	}
	results2 := a2.Results()
	if len(results2) != 1 {
		t.Fatalf("Results len = %d, want 1", len(results2))
	}
	if got := len(results2[0].Repos); got != 3 {
		t.Errorf("Repos count = %d, want 3", got)
	}
}

func TestAggregatorSkipsBots(t *testing.T) {
	a := NewAggregator(time.Now())

	events := []Event{
		{Type: "PullRequestEvent", Action: "opened", Actor: "alice", Repo: "org/repo"},
		{Type: "PullRequestEvent", Action: "opened", Actor: "dependabot[bot]", Repo: "org/repo"},
		{Type: "PullRequestReviewEvent", Action: "submitted", Actor: "renovate[bot]", Repo: "org/repo"},
		{Type: "IssueCommentEvent", Action: "created", Actor: "copilot", Repo: "org/repo"},
		{Type: "PullRequestEvent", Action: "opened", Actor: "bob", Repo: "org/repo"},
	}
	for _, ev := range events {
		a.Add(ev)
	}

	if got := a.Count(); got != 2 {
		t.Fatalf("Count = %d, want 2 (bots filtered)", got)
	}
	results := a.Results()
	for _, s := range results {
		if s.Username == "dependabot[bot]" || s.Username == "renovate[bot]" || s.Username == "copilot" {
			t.Errorf("bot %q should have been filtered", s.Username)
		}
	}
}

func TestAggregatorIssuesEvent(t *testing.T) {
	a := NewAggregator(time.Now())

	events := []Event{
		{Type: "IssuesEvent", Action: "opened", Actor: "alice", Repo: "org/repo1"},
		{Type: "IssuesEvent", Action: "opened", Actor: "alice", Repo: "org/repo1"},
		{Type: "IssuesEvent", Action: "closed", Actor: "alice", Repo: "org/repo1"},
		{Type: "IssuesEvent", Action: "reopened", Actor: "alice", Repo: "org/repo1"}, // ignored
	}
	for _, ev := range events {
		a.Add(ev)
	}

	results := a.Results()
	if len(results) != 1 {
		t.Fatalf("Results len = %d, want 1", len(results))
	}

	s := results[0]
	if s.IssuesOpened != 2 {
		t.Errorf("IssuesOpened = %d, want 2", s.IssuesOpened)
	}
	if s.IssuesClosed != 1 {
		t.Errorf("IssuesClosed = %d, want 1", s.IssuesClosed)
	}
}

func TestAggregatorHourTruncation(t *testing.T) {
	input := time.Date(2026, 3, 15, 8, 30, 45, 0, time.UTC)
	a := NewAggregator(input)

	want := time.Date(2026, 3, 15, 8, 0, 0, 0, time.UTC)
	if got := a.Hour(); !got.Equal(want) {
		t.Errorf("Hour() = %v, want %v", got, want)
	}
}
