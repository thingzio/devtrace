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

// PREvent is one observation of a PullRequestEvent, persisted to the
// merge-graph table so opened/merged/closed actions on the same PR
// can be joined later. Without this, "PRs merged authored by X"
// can't be computed: GH Archive attributes the merged action to
// whoever clicked merge (usually a CI bot), not the PR author.
//
// Per-event records are keyed by (repo, pr_number) at storage
// time; the storage layer COALESCEs fields so each action stamps
// only its own column without clobbering earlier observations of
// the same PR.
type PREvent struct {
	Repo    string
	Number  int
	Action  string    // opened, merged, closed
	Author  string    // populated for opened action; empty otherwise
	OccurAt time.Time // event timestamp
}

// prEventKey identifies a unique PR within an hour, used to dedupe
// repeated events on the same PR within a single archive hour
// (a PR being opened then immediately closed in the same hour
// is rare but possible; we keep the first observation per action
// to avoid double-counting).
type prEventKey struct {
	Repo   string
	Number int
	Action string
}

// Aggregator collects events into per-contributor hourly summaries
// and per-PR observations for the merge graph.
type Aggregator struct {
	hour      time.Time
	summaries map[string]*Summary // key: username
	prEvents  map[prEventKey]PREvent
}

// NewAggregator creates an aggregator for the given hour (truncated to the hour boundary).
func NewAggregator(hour time.Time) *Aggregator {
	return &Aggregator{
		hour:      hour.Truncate(time.Hour),
		summaries: make(map[string]*Summary),
		prEvents:  make(map[prEventKey]PREvent),
	}
}

// Add processes a single event. Bot actors are skipped for the
// per-contributor summaries (we don't credit bots with activity)
// but PullRequestEvent observations are recorded in the merge graph
// regardless of actor — the merge action's actor is typically a CI
// bot, and that observation IS the merge graph's reason to exist.
func (a *Aggregator) Add(ev Event) {
	// Capture PR-level observations before the bot filter — the bot
	// IS the merge actor in modern OSS workflows, so filtering here
	// would drop the very signal we need to correlate later. The
	// author of an "opened" event is still a human (we don't insert
	// PRs opened by bots into the merge graph because the action
	// fires the bot filter below before we record the author), so
	// we only stash the actor on the opened path.
	if ev.Type == EventPullRequest && ev.Repo != "" && ev.PRNumber > 0 {
		switch ev.Action {
		case actionOpened, actionMerged, actionClosed:
			key := prEventKey{Repo: ev.Repo, Number: ev.PRNumber, Action: ev.Action}
			if _, dup := a.prEvents[key]; !dup {
				rec := PREvent{
					Repo:    ev.Repo,
					Number:  ev.PRNumber,
					Action:  ev.Action,
					OccurAt: ev.CreatedAt,
				}
				// Only record an author for opened — and only when the
				// actor is a human. Bot-opened PRs (Renovate, Dependabot)
				// aren't credited as authored merges later.
				if ev.Action == actionOpened && !bot.IsBot(ev.Actor) {
					rec.Author = ev.Actor
				}
				a.prEvents[key] = rec
			}
		}
	}

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

// PREvents returns all collected PR-level observations for this
// hour. Each (repo, pr_number, action) tuple is unique within an
// hour; the storage layer COALESCEs across hours into a single PR
// row whose author / opened_at / merged_at / closed_at columns
// fill in as the corresponding events arrive.
func (a *Aggregator) PREvents() []PREvent {
	out := make([]PREvent, 0, len(a.prEvents))
	for _, ev := range a.prEvents {
		out = append(out, ev)
	}
	return out
}

// Count returns the number of unique contributors aggregated.
func (a *Aggregator) Count() int {
	return len(a.summaries)
}
