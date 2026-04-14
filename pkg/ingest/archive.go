package ingest

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const defaultBaseURL = "https://data.gharchive.org"

type Event struct {
	Type      string
	Action    string
	Actor     string
	Repo      string
	CreatedAt time.Time
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

// Stream downloads the archive for the given hour and calls fn for each relevant event.
func (r *ArchiveReader) Stream(ctx context.Context, hour time.Time, fn func(Event)) error {
	url := r.URL(hour)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("archive %s: status %d", url, resp.StatusCode)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	scanner := bufio.NewScanner(gz)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20) // 1MB line buffer
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
	case "PullRequestEvent", "PullRequestReviewEvent", "IssueCommentEvent":
	default:
		return Event{}, false
	}
	var payload struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(raw.Payload, &payload)
	t, _ := time.Parse(time.RFC3339, raw.CreatedAt)
	return Event{
		Type:      raw.Type,
		Action:    payload.Action,
		Actor:     raw.Actor.Login,
		Repo:      raw.Repo.Name,
		CreatedAt: t,
	}, raw.Actor.Login != "" && raw.Repo.Name != ""
}
