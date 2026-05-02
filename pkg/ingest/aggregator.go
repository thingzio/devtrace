package ingest

import (
	"time"

	"github.com/thingzio/devtrace/pkg/bot"
)

const (
	actionOpened = "opened"
	actionClosed = "closed"
	// GH Archive emits a distinct "merged" action for merged PRs — it
	// does NOT use action=closed + payload.pull_request.merged=true.
	// The pull_request object in archive payloads only carries thin
	// metadata (url, id, number, head, base); no merged flag exists
	// to read. Detected after a contributor with known merged PRs
	// showed PRsMerged=0; survey of one hour found 936 events with
	// action=merged and zero with payload.pull_request.merged=true.
	actionMerged = "merged"
)

// Summary is the per-contributor hourly aggregation result.
type Summary struct {
	Username      string
	PRsOpened     int
	PRsMerged     int
	PRsClosed     int
	ReviewsGiven  int
	IssueComments int
	IssuesOpened  int
	IssuesClosed  int
	Repos         map[string]bool
}

// Aggregator collects events into per-contributor hourly summaries.
type Aggregator struct {
	hour      time.Time
	summaries map[string]*Summary // key: username
}

// NewAggregator creates an aggregator for the given hour (truncated to the hour boundary).
func NewAggregator(hour time.Time) *Aggregator {
	return &Aggregator{
		hour:      hour.Truncate(time.Hour),
		summaries: make(map[string]*Summary),
	}
}

// Add processes a single event. Bot actors are silently skipped.
func (a *Aggregator) Add(ev Event) {
	if bot.IsBot(ev.Actor) {
		return
	}

	s, ok := a.summaries[ev.Actor]
	if !ok {
		s = &Summary{
			Username: ev.Actor,
			Repos:    make(map[string]bool),
		}
		a.summaries[ev.Actor] = s
	}
	s.Repos[ev.Repo] = true

	switch ev.Type {
	case EventPullRequest:
		// "opened", "merged", and "closed" are the actions tracked.
		// "reopened" is intentionally ignored because it does not
		// represent a new PR; counting it would inflate the
		// contributor's PR velocity and distort scoring. After fix:
		// action=merged routes to PRsMerged, action=closed to
		// PRsClosed (closed without merging) — these are mutually
		// exclusive in GH Archive.
		switch ev.Action {
		case actionOpened:
			s.PRsOpened++
		case actionMerged:
			s.PRsMerged++
		case actionClosed:
			s.PRsClosed++
		}
	case EventPullRequestReview:
		s.ReviewsGiven++
	case EventIssueComment:
		s.IssueComments++
	case EventIssues:
		switch ev.Action {
		case actionOpened:
			s.IssuesOpened++
		case actionClosed:
			s.IssuesClosed++
		}
	}
}

// Hour returns the hour this aggregator is collecting for.
func (a *Aggregator) Hour() time.Time {
	return a.hour
}

// Results returns all summaries.
func (a *Aggregator) Results() []Summary {
	results := make([]Summary, 0, len(a.summaries))
	for _, s := range a.summaries {
		results = append(results, *s)
	}
	return results
}

// Count returns the number of unique contributors aggregated.
func (a *Aggregator) Count() int {
	return len(a.summaries)
}
