package claude

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientNoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("DEVTRACE_ANTHROPIC_API_KEY", "")
	if c := New(); c != nil {
		t.Fatal("expected nil client when no API key is set")
	}
}

func TestNewClientWithKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_MODEL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")

	c := New()
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.model != defaultModel {
		t.Errorf("model: got %q, want %q", c.model, defaultModel)
	}
	if c.baseURL != defaultBaseURL {
		t.Errorf("baseURL: got %q, want %q", c.baseURL, defaultBaseURL)
	}
}

func TestNewClientCustomModel(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_MODEL", "claude-sonnet-4-20250514")

	c := New()
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.model != "claude-sonnet-4-20250514" {
		t.Errorf("model: got %q, want claude-sonnet-4-20250514", c.model)
	}
}

func newTestClient(url string) *Client {
	return &Client{
		apiKey:  "test-key",
		model:   defaultModel,
		baseURL: url,
		http:    http.DefaultClient,
	}
}

// cannedResponse returns a valid Anthropic Messages API response.
func cannedResponse(text string) []byte {
	resp := struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Model string `json:"model"`
		Role  string `json:"role"`
	}{
		Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: text}},
		Model: defaultModel,
		Role:  "assistant",
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestGenerateRiskNarrative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s, want POST", r.Method)
		}
		if r.URL.Path != "/messages" {
			t.Errorf("path: got %s, want /messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key: got %q, want test-key", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicVersion {
			t.Errorf("anthropic-version: got %q, want %s", got, anthropicVersion)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(cannedResponse("This contributor shows low risk based on account age and merge history."))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	narrative, err := c.GenerateRiskNarrative(context.Background(), RiskInput{
		Username:    "testuser",
		Score:       0.85,
		Grade:       "A",
		AccountAge:  365,
		PRsMerged:   42,
		PublicRepos: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "This contributor shows low risk based on account age and merge history."
	if narrative != want {
		t.Errorf("narrative: got %q, want %q", narrative, want)
	}
}

func TestClassifyPRAuthenticity(t *testing.T) {
	classification := AuthenticityResult{
		Classification: "human",
		Confidence:     0.92,
		Reasoning:      "Description uses casual tone with specific context.",
	}
	classJSON, _ := json.Marshal(classification)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(cannedResponse(string(classJSON)))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	result, err := c.ClassifyPRAuthenticity(context.Background(), []string{
		"Fixed the off-by-one in the loop — was driving me crazy for an hour.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Classification != "human" {
		t.Errorf("classification: got %q, want human", result.Classification)
	}
	if result.Confidence != 0.92 {
		t.Errorf("confidence: got %f, want 0.92", result.Confidence)
	}
}

func TestCompleteAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"too many requests"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.GenerateRiskNarrative(context.Background(), RiskInput{Username: "x"})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
}
