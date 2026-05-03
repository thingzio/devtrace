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

// TestAggregatorPREventsCorrelateAuthor pins the merge-graph
// behavior: the opened action attributes the PR to its human author,
// but merged/closed events from any actor (including bots) record
// only the timestamp on the same (repo, pr_number) record. This is
// the foundation for "PRs authored by X that got merged" — at
// storage time the records merge so author + opened_at + merged_at
// land on a single row.
func TestAggregatorPREventsCorrelateAuthor(t *testing.T) {
	a := NewAggregator(time.Now())
	repo := "org/repo"
	a.Add(Event{Type: "PullRequestEvent", Action: "opened", Actor: "human-author", Repo: repo, PRNumber: 42, CreatedAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)})
	a.Add(Event{Type: "PullRequestEvent", Action: "merged", Actor: "github-actions[bot]", Repo: repo, PRNumber: 42, CreatedAt: time.Date(2026, 3, 5, 18, 0, 0, 0, time.UTC)})

	pre := a.PREvents()
	if len(pre) != 2 {
		t.Fatalf("PR events: got %d, want 2", len(pre))
	}
	var opened, merged *PREvent
	for i := range pre {
		switch pre[i].Action {
		case "opened":
			opened = &pre[i]
		case "merged":
			merged = &pre[i]
		}
	}
	if opened == nil || opened.Author != "human-author" {
		t.Errorf("opened event must carry the human author, got %+v", opened)
	}
	if merged == nil || merged.Author != "" {
		t.Errorf("merged event must NOT carry an author (the bot is filtered), got %+v", merged)
	}
	if opened.Number != 42 || merged.Number != 42 {
		t.Errorf("PR numbers mismatch: opened=%d merged=%d", opened.Number, merged.Number)
	}
}

// TestAggregatorPREventsBotOpenerNotAttributed asserts that PRs
// opened by bots (Renovate, Dependabot) do NOT carry an Author
// through to the merge graph: we don't credit bots with merged-PR
// authorship later. The opened observation still gets recorded so
// the PR is known to exist; just without an author.
func TestAggregatorPREventsBotOpenerNotAttributed(t *testing.T) {
	a := NewAggregator(time.Now())
	a.Add(Event{Type: "PullRequestEvent", Action: "opened", Actor: "renovate[bot]", Repo: "org/repo", PRNumber: 7})

	pre := a.PREvents()
	if len(pre) != 1 {
		t.Fatalf("PR events: got %d, want 1", len(pre))
	}
	if pre[0].Author != "" {
		t.Errorf("bot-opened PR must have empty Author, got %q", pre[0].Author)
	}
}

// TestAggregatorPREventsDedupeWithinHour: a single (repo, number,
// action) tuple recorded only once per hour even if duplicate events
// arrive (which can happen with archive replays). The summary path's
// per-actor counts are unaffected — this dedupe is specific to the
// merge graph where double-counting an open or merge is a real risk.
func TestAggregatorPREventsDedupeWithinHour(t *testing.T) {
	a := NewAggregator(time.Now())
	repo := "org/repo"
	a.Add(Event{Type: "PullRequestEvent", Action: "merged", Actor: "actor-1", Repo: repo, PRNumber: 1})
	a.Add(Event{Type: "PullRequestEvent", Action: "merged", Actor: "actor-2", Repo: repo, PRNumber: 1})
	a.Add(Event{Type: "PullRequestEvent", Action: "merged", Actor: "actor-3", Repo: repo, PRNumber: 1})

	pre := a.PREvents()
	if len(pre) != 1 {
		t.Errorf("PR events should dedupe to 1 per (repo, number, action), got %d", len(pre))
	}
}

// TestAggregatorPREventsSkipsZeroNumber: events without a PR number
// (other event types, or malformed PullRequestEvent) don't pollute
// the merge graph.
func TestAggregatorPREventsSkipsZeroNumber(t *testing.T) {
	a := NewAggregator(time.Now())
	a.Add(Event{Type: "IssuesEvent", Action: "opened", Actor: "u", Repo: "org/r"})
	a.Add(Event{Type: "PullRequestEvent", Action: "opened", Actor: "u", Repo: "org/r", PRNumber: 0})
	if got := len(a.PREvents()); got != 0 {
		t.Errorf("expected 0 PR events, got %d", got)
	}
}

// TestAggregatorMergedAction pins the action="merged" → PRsMerged
// routing. GH Archive emits a distinct "merged" action when a PR is
// merged; we historically watched for action="closed" with a separate
// merged flag, which never matched. Real archives never set
// payload.pull_request.merged, so the previous logic produced
// PRsMerged=0 for every contributor.
func TestAggregatorMergedAction(t *testing.T) {
	a := NewAggregator(time.Now())
	events := []Event{
		{Type: "PullRequestEvent", Action: "opened", Actor: "alice", Repo: "org/repo1"},
		{Type: "PullRequestEvent", Action: "merged", Actor: "alice", Repo: "org/repo1"},
		{Type: "PullRequestEvent", Action: "merged", Actor: "alice", Repo: "org/repo1"},
		{Type: "PullRequestEvent", Action: "closed", Actor: "alice", Repo: "org/repo1"},
	}
	for _, ev := range events {
		a.Add(ev)
	}
	results := a.Results()
	if len(results) != 1 {
		t.Fatalf("Results len = %d, want 1", len(results))
	}
	s := results[0]
	if s.PRsOpened != 1 {
		t.Errorf("PRsOpened = %d, want 1", s.PRsOpened)
	}
	if s.PRsMerged != 2 {
		t.Errorf("PRsMerged = %d, want 2 (action=merged)", s.PRsMerged)
	}
	if s.PRsClosed != 1 {
		t.Errorf("PRsClosed = %d, want 1 (action=closed only counts unmerged closes)", s.PRsClosed)
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
