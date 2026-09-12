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

package server

import (
	"strings"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

func TestBuildBars_Empty(t *testing.T) {
	if got := buildBars(nil, nil, "15:04"); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestBuildBars_PercentScalingAndFloor(t *testing.T) {
	now := time.Now()
	times := []time.Time{now.Add(-2 * time.Hour), now.Add(-time.Hour), now}
	counts := []int{50, 100, 1}

	bars := buildBars(times, counts, "15:04")
	if len(bars) != 3 {
		t.Fatalf("len = %d, want 3", len(bars))
	}
	// Peak count = 100 → 100%, second = 50%, lowest pinned to 2% floor.
	if bars[1].Percent != 100 {
		t.Errorf("peak Percent = %d, want 100", bars[1].Percent)
	}
	if bars[0].Percent != 50 {
		t.Errorf("middle Percent = %d, want 50", bars[0].Percent)
	}
	if bars[2].Percent != 2 {
		t.Errorf("trough Percent = %d, want 2 (floor)", bars[2].Percent)
	}
	for i, b := range bars {
		if b.UTC == "" {
			t.Errorf("bars[%d].UTC empty — clients need it for local time conversion", i)
		}
	}
}

func TestBuildBars_AllZerosKeepsFloor(t *testing.T) {
	times := []time.Time{time.Now(), time.Now().Add(time.Hour)}
	bars := buildBars(times, []int{0, 0}, "15:04")
	for i, b := range bars {
		if b.Percent != 2 {
			t.Errorf("bars[%d].Percent = %d, want 2 (floor)", i, b.Percent)
		}
	}
}

func TestHourlyCountBars_Empty(t *testing.T) {
	if got := hourlyCountBars(nil); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestHourlyCountBars_PassesThroughToBuildBars(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	hc := []postgres.HourlyCount{
		{Day: now.Add(-2 * time.Hour), Count: 5},
		{Day: now.Add(-time.Hour), Count: 10},
	}
	bars := hourlyCountBars(hc)
	if len(bars) != 2 {
		t.Fatalf("len = %d, want 2", len(bars))
	}
	if bars[1].Count != 10 || bars[0].Count != 5 {
		t.Errorf("counts not preserved: %+v", bars)
	}
}

func TestHourlyCountBars24_FillsGaps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	// One real data point in the middle; the other 23 hours must be
	// zero-filled so the chart shows a complete 24h window.
	hc := []postgres.HourlyCount{{Day: now.Add(-12 * time.Hour), Count: 7}}

	bars := hourlyCountBars24(hc)
	if len(bars) != 24 {
		t.Fatalf("len = %d, want 24", len(bars))
	}
	var found int
	for _, b := range bars {
		if b.Count == 7 {
			found++
		}
	}
	if found != 1 {
		t.Errorf("seeded count appeared %d times, want 1", found)
	}
}

func TestSampleDigestEvents_HasCorrectShape(t *testing.T) {
	events := sampleDigestEvents()
	if len(events) != 2 {
		t.Fatalf("len = %d, want 2 sample events", len(events))
	}
	// Sample events must cover both event types so admins see what
	// each renders like in the digest preview.
	types := map[string]bool{}
	for _, e := range events {
		types[e.EventType] = true
	}
	if !types[postgres.EventTypeNewContributor] {
		t.Error("missing new-contributor sample event")
	}
	if !types[postgres.EventTypeScoreChange] {
		t.Error("missing score-change sample event")
	}
}

func TestBuildAdminUnsubscribeURL_NoSecretReturnsEmpty(t *testing.T) {
	t.Setenv("DIGEST_HMAC_SECRET", "")
	if url := buildAdminUnsubscribeURL("https://example.test", "tenant-1"); url != "" {
		t.Errorf("expected empty URL when secret not set, got %q", url)
	}
}

func TestBuildAdminUnsubscribeURL_WithSecretContainsTenantAndToken(t *testing.T) {
	t.Setenv("DIGEST_HMAC_SECRET", "test-secret-value")
	url := buildAdminUnsubscribeURL("https://example.test", "tenant-xyz")
	if url == "" {
		t.Fatal("expected non-empty URL")
	}
	if !strings.Contains(url, "tenant=tenant-xyz") {
		t.Errorf("URL %q missing tenant param", url)
	}
	if !strings.Contains(url, "token=") {
		t.Errorf("URL %q missing token param", url)
	}
	if !strings.HasPrefix(url, "https://example.test/digest/unsubscribe?") {
		t.Errorf("URL %q has wrong prefix", url)
	}
}
