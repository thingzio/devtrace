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

package ingest

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrArchiveNotFound is returned when a GH Archive hourly file is not yet published.
var ErrArchiveNotFound = errors.New("archive not found")

const defaultBaseURL = "https://data.gharchive.org"

// archiveDownloadRetries bounds how many times Stream retries on transient
// upstream failures (HTTP 5xx, connection resets). 404 is fast-fail —
// later hours won't be available either, so callers stop the loop. The
// modest cap keeps backfill making progress while still recovering from
// brief CDN hiccups.
const archiveDownloadRetries = 2

// archiveRetryBaseDelay is the base wait between retries; doubles each
// attempt with no jitter (one caller per hour, no thundering herd).
const archiveRetryBaseDelay = 2 * time.Second

// GitHub event type constants.
const (
	EventPullRequest       = "PullRequestEvent"
	EventPullRequestReview = "PullRequestReviewEvent"
	EventIssueComment      = "IssueCommentEvent"
	EventIssues            = "IssuesEvent"
)

type Event struct {
	Type      string
	Action    string
	Actor     string
	Repo      string
	CreatedAt time.Time
	// PRNumber is populated for PullRequestEvent only; zero for other
	// event types. Used by the merge-graph aggregator to correlate
	// opened/merged/closed events on the same PR (the actor differs:
	// opens are attributed to the author, merges are usually attributed
	// to a CI bot in modern OSS workflows).
	PRNumber int
}

type ArchiveReader struct {
	baseURL string
	client  *http.Client
}

func NewArchiveReader(baseURL string) *ArchiveReader {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &ArchiveReader{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
}

func (r *ArchiveReader) URL(t time.Time) string {
	return fmt.Sprintf("%s/%d-%02d-%02d-%d.json.gz",
		r.baseURL, t.UTC().Year(), t.UTC().Month(), t.UTC().Day(), t.UTC().Hour())
}

// Stream downloads the archive for the given hour and calls fn for each
// relevant event. Transient transport failures and 5xx responses are
// retried with exponential backoff; 404 short-circuits as
// ErrArchiveNotFound (the file is not yet published, later hours won't
// be either).
func (r *ArchiveReader) Stream(ctx context.Context, hour time.Time, fn func(Event)) error {
	url := r.URL(hour)
	var lastErr error
	for attempt := 0; attempt <= archiveDownloadRetries; attempt++ {
		if attempt > 0 {
			wait := archiveRetryBaseDelay << (attempt - 1)
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		err := r.streamOnce(ctx, url, fn)
		if err == nil {
			return nil
		}
		// Permanent: not-found and ctx cancellation.
		if errors.Is(err, ErrArchiveNotFound) || ctx.Err() != nil {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("archive %s: %d retries exhausted: %w", url, archiveDownloadRetries, lastErr)
}

// streamOnce performs a single attempt; called by Stream within its retry
// loop. Splitting it keeps the retry control flow legible and avoids a
// nested labeled break.
func (r *ArchiveReader) streamOnce(ctx context.Context, url string, fn func(Event)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("archive %s: %w", url, ErrArchiveNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("archive %s: status %d", url, resp.StatusCode)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	scanner := bufio.NewScanner(gz)
	scanner.Buffer(make([]byte, 0, 1<<20), 8<<20) // 8MB max line buffer
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ev, ok := parseEvent(scanner.Bytes())
		if ok {
			fn(ev)
		}
	}
	return scanner.Err()
}

type rawEvent struct {
	Type  string `json:"type"`
	Actor struct {
		Login string `json:"login"`
	} `json:"actor"`
	Repo struct {
		Name string `json:"name"`
	} `json:"repo"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

func parseEvent(line []byte) (Event, bool) {
	var raw rawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return Event{}, false
	}
	switch raw.Type {
	case EventPullRequest, EventPullRequestReview, EventIssueComment, EventIssues:
	default:
		return Event{}, false
	}
	// PullRequestEvent payloads carry a top-level `number` field with
	// the PR number; the merge-graph aggregator needs it to correlate
	// opened/merged/closed events on the same PR. Other event types
	// don't need PR numbers, but the field harmlessly stays zero.
	var payload struct {
		Action string `json:"action"`
		Number int    `json:"number"`
	}
	_ = json.Unmarshal(raw.Payload, &payload)
	t, _ := time.Parse(time.RFC3339, raw.CreatedAt)
	return Event{
		Type:      raw.Type,
		Action:    payload.Action,
		Actor:     raw.Actor.Login,
		Repo:      raw.Repo.Name,
		CreatedAt: t,
		PRNumber:  payload.Number,
	}, raw.Actor.Login != "" && raw.Repo.Name != ""
}
