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
	Repos       []Repo
	Credits     []SecurityAdvisoryCredit
	Err         error
}

func (m *MockClient) FetchUser(_ context.Context, _ string) (*UserProfile, error) {
	return m.UserProfile, m.Err
}

func (m *MockClient) FetchSignals(_ context.Context, _, _ string, _ *ArchiveHints) (*score.InputSignals, error) {
	return m.Signals, m.Err
}

func (m *MockClient) IsOrgMember(_ context.Context, _, _ string) (bool, error) {
	return false, m.Err
}

func (m *MockClient) ListUserRepos(_ context.Context, _ string, _ int) ([]Repo, error) {
	return m.Repos, m.Err
}

func (m *MockClient) FetchSecurityCredits(_ context.Context, _ string, _ int) ([]SecurityAdvisoryCredit, error) {
	return m.Credits, m.Err
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
