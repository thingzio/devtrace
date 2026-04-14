package net

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	resendAPIURL     = "https://api.resend.com/emails"
	emailSendTimeout = 30 * time.Second
)

var emailClient = &http.Client{Timeout: emailSendTimeout}

// SendEmail sends an email via the Resend API. The apiKey is a Resend API key.
// from and to are email addresses; subject, html, and text are the message content.
// replyTo is optional — if non-empty, sets the Reply-To header on the email.
func SendEmail(ctx context.Context, apiKey, from, to, subject, html, text, replyTo string) error {
	payload := map[string]any{
		"from":    from,
		"to":      []string{to},
		"subject": subject,
		"html":    html,
		"text":    text,
	}
	if replyTo != "" {
		payload["reply_to"] = replyTo
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling email payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendAPIURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating email request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := emailClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending email: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	truncated := string(respBody)
	if len(truncated) > 200 {
		truncated = truncated[:200]
	}
	return fmt.Errorf("email API returned %d: %s", resp.StatusCode, truncated)
}
