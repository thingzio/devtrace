package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	defaultBaseURL   = "https://api.anthropic.com/v1"
	defaultModel     = "claude-haiku-4-5"
	anthropicVersion = "2023-06-01"
)

// Client wraps the Anthropic Messages API. Nil-safe — callers should
// check for nil before use to allow graceful degradation.
type Client struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

// New creates a Claude client from environment variables.
// Checks DEVTRACE_ANTHROPIC_API_KEY first (service-scoped), then ANTHROPIC_API_KEY.
// Returns nil if neither is set.
func New() *Client {
	key := os.Getenv("DEVTRACE_ANTHROPIC_API_KEY")
	if key == "" {
		key = os.Getenv("ANTHROPIC_API_KEY")
	}
	if key == "" {
		return nil
	}

	model := os.Getenv("DEVTRACE_ANTHROPIC_MODEL")
	if model == "" {
		model = os.Getenv("ANTHROPIC_MODEL")
	}
	if model == "" {
		model = defaultModel
	}

	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return &Client{
		apiKey:  key,
		model:   model,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// RiskInput contains the data for risk narrative generation.
type RiskInput struct {
	Username       string             `json:"username"`
	Score          float64            `json:"score"`
	Grade          string             `json:"grade"`
	Categories     map[string]float64 `json:"categories"`
	AccountAge     int64              `json:"account_age_days"`
	PRsMerged      int64              `json:"prs_merged"`
	PRsClosed      int64              `json:"prs_closed"`
	Followers      int64              `json:"followers"`
	PublicRepos    int64              `json:"public_repos"`
	Suspended      bool               `json:"suspended"`
	HasRepoContext bool               `json:"has_repo_context"`
	RepoContext    string             `json:"repo_context,omitempty"`
}

// AuthenticityResult is the classification result for PR descriptions.
type AuthenticityResult struct {
	Classification string  `json:"classification"` // human, ai_assisted, ai_generated, uncertain
	Confidence     float64 `json:"confidence"`
	Reasoning      string  `json:"reasoning"`
}

const riskSystemPrompt = `You are a security analyst assessing open source contributor reputation.
Given a contributor's scoring signals, produce a 1-2 sentence factual assessment.

Category scores are WEIGHTED CONTRIBUTIONS to the total (not percentages).
Maximum possible per category: code_provenance=0.15, identity=0.25, engagement=0.25, community=0.15, behavioral=0.20.
A category score near its max is STRONG, not weak. Example: behavioral=0.20 means perfect behavioral score.

Categories showing 0.00 without repo_context means data was NOT AVAILABLE (no repo to evaluate), not a negative signal. Do not treat missing data as concerning.

When repo_context is empty, focus on the contributor's global reputation (account age, PRs, community standing). Do not use PR review language like "review this PR" or "before merging".

Be factual and constructive. Highlight genuine strengths. Only flag actual concerns backed by specific signal values. Do not speculate or assume negative intent. Do not use markdown formatting.`

const authenticitySystemPrompt = `Classify the following PR descriptions as: human, ai_assisted, ai_generated, or uncertain.
AI-generated descriptions tend to: use bullet points exhaustively, explain "what" but omit "why",
follow templates, and use overly formal language.
Return ONLY valid JSON: {"classification": "...", "confidence": 0.0-1.0, "reasoning": "one sentence"}`

// GenerateRiskNarrative produces a 1-2 sentence risk assessment.
func (c *Client) GenerateRiskNarrative(ctx context.Context, input RiskInput) (string, error) {
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal input: %w", err)
	}

	return c.complete(ctx, riskSystemPrompt, string(inputJSON), 256)
}

// ClassifyPRAuthenticity classifies PR descriptions.
func (c *Client) ClassifyPRAuthenticity(ctx context.Context, descriptions []string) (*AuthenticityResult, error) {
	input, err := json.Marshal(descriptions)
	if err != nil {
		return nil, fmt.Errorf("marshal descriptions: %w", err)
	}

	resp, err := c.complete(ctx, authenticitySystemPrompt, string(input), 256)
	if err != nil {
		return nil, err
	}

	var result AuthenticityResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("parse classification: %w", err)
	}

	return &result, nil
}

// messagesRequest is the Anthropic Messages API request body.
type messagesRequest struct {
	Model    string          `json:"model"`
	MaxToks  int             `json:"max_tokens"`
	System   string          `json:"system"`
	Messages []messagesEntry `json:"messages"`
}

type messagesEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// messagesResponse is the Anthropic Messages API response body.
type messagesResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// complete sends a message to the Anthropic Messages API and returns the text.
func (c *Client) complete(ctx context.Context, system, userMessage string, maxTokens int) (string, error) {
	body := messagesRequest{
		Model:   c.model,
		MaxToks: maxTokens,
		System:  system,
		Messages: []messagesEntry{
			{Role: "user", Content: userMessage},
		},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/messages", bytes.NewReader(bodyJSON))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api error: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result messagesResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response content")
	}

	return result.Content[0].Text, nil
}
