package github

import (
	"context"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/score"
)

// MockClient is an exported mock for use in downstream packages (e.g., score service tests).
type MockClient struct {
	UserProfile *UserProfile
	Signals     *score.InputSignals
	Err         error
}

func (m *MockClient) FetchUser(_ context.Context, _ string) (*UserProfile, error) {
	return m.UserProfile, m.Err
}

func (m *MockClient) FetchSignals(_ context.Context, _, _ string, _ *ArchiveHints) (*score.InputSignals, error) {
	return m.Signals, m.Err
}

func TestMockClientImplementsInterface(t *testing.T) {
	var _ Client = &MockClient{}
	var _ Client = &PATClient{}
}

func TestMockClientReturnsConfiguredValues(t *testing.T) {
	now := time.Now()
	profile := &UserProfile{
		Username:  "testuser",
		Name:      "Test User",
		CreatedAt: now,
		Followers: 42,
	}
	signals := &score.InputSignals{
		AgeDays:   365,
		PRsMerged: 10,
		Followers: 42,
	}

	mc := &MockClient{
		UserProfile: profile,
		Signals:     signals,
	}

	ctx := context.Background()

	gotProfile, err := mc.FetchUser(ctx, "testuser")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotProfile.Username != "testuser" {
		t.Errorf("want username testuser, got %s", gotProfile.Username)
	}
	if gotProfile.Followers != 42 {
		t.Errorf("want followers 42, got %d", gotProfile.Followers)
	}

	gotSignals, err := mc.FetchSignals(ctx, "testuser", "org/repo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSignals.PRsMerged != 10 {
		t.Errorf("want PRsMerged 10, got %d", gotSignals.PRsMerged)
	}
}
