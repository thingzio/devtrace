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
)

func TestFormatTimestamp_Zero(t *testing.T) {
	if got := formatTimestamp(time.Time{}); got != durationNever {
		t.Errorf("zero timestamp = %q, want %q", got, durationNever)
	}
}

func TestFormatTimestamp_Concrete(t *testing.T) {
	in := time.Date(2026, 5, 3, 12, 34, 56, 0, time.UTC)
	got := formatTimestamp(in)
	want := "2026-05-03 12:34 UTC"
	if got != want {
		t.Errorf("formatTimestamp(%v) = %q, want %q", in, got, want)
	}
}

func TestFormatTimeSeries_ParseError(t *testing.T) {
	if got := formatTimeSeries("not json"); !strings.Contains(got, "parse error") {
		t.Errorf("expected parse-error sentinel, got %q", got)
	}
}

func TestFormatTimeSeries_NoData(t *testing.T) {
	if got := formatTimeSeries(`{"timeSeries":[]}`); !strings.Contains(got, "no data") {
		t.Errorf("expected no-data sentinel, got %q", got)
	}
}

func TestFormatTimeSeries_DoubleAndIntPoints(t *testing.T) {
	// Mix of doubleValue and int64Value points across two time series with
	// labels — exercises the prefix label formatting and both numeric paths.
	body := `{
	  "timeSeries": [
	    {
	      "metric": {"labels": {"response_code_class":"2xx"}},
	      "points": [
	        {"interval":{"startTime":"2026-05-03T10:00:00Z"}, "value":{"doubleValue":1.5}},
	        {"interval":{"startTime":"2026-05-03T11:00:00Z"}, "value":{"int64Value":"42"}}
	      ]
	    },
	    {
	      "metric": {"labels": {}},
	      "points": [
	        {"interval":{"startTime":"2026-05-03T10:00:00Z"}, "value":{"doubleValue":3.14}}
	      ]
	    }
	  ]
	}`
	got := formatTimeSeries(body)
	if !strings.Contains(got, "2xx") {
		t.Errorf("output missing label prefix; got %q", got)
	}
	if !strings.Contains(got, "1.50") {
		t.Errorf("output missing double value; got %q", got)
	}
	if !strings.Contains(got, "42.00") {
		t.Errorf("output missing int64 value; got %q", got)
	}
	if !strings.Contains(got, "3.14") {
		t.Errorf("output missing second time-series; got %q", got)
	}
}

func TestFormatTimeSeries_TruncatesLongTimestamps(t *testing.T) {
	body := `{"timeSeries":[{"metric":{"labels":{}},"points":[{"interval":{"startTime":"2026-05-03T10:30:45.123Z"},"value":{"doubleValue":1}}]}]}`
	got := formatTimeSeries(body)
	// Format keeps a 11-char window starting at index 5 (e.g. "05-03T10:30").
	if !strings.Contains(got, "05-03T10:30") {
		t.Errorf("expected timestamp window; got %q", got)
	}
}

func TestFormatTimeSeries_PointLimit(t *testing.T) {
	// Build 12 points; output should only include the first 8.
	var points []string
	for i := 0; i < 12; i++ {
		points = append(points,
			`{"interval":{"startTime":"2026-05-03T10:00:00Z"},"value":{"doubleValue":1}}`)
	}
	body := `{"timeSeries":[{"metric":{"labels":{}},"points":[` +
		strings.Join(points, ",") + `]}]}`
	got := formatTimeSeries(body)
	count := strings.Count(got, "=1.00")
	if count != 8 {
		t.Errorf("rendered %d points, want 8 (first 8 only)", count)
	}
}

func TestAdminMetricQueries_AllReferenceService(t *testing.T) {
	cfg := &adminMetricsConfig{service: "test-service-name"}
	queries := adminMetricQueries(cfg, "alignmentPeriod=3600s")
	if len(queries) == 0 {
		t.Fatal("expected at least one metric query")
	}
	for i, q := range queries {
		if q.label == "" {
			t.Errorf("query[%d] missing label", i)
		}
		if !strings.Contains(q.filter, `service_name="test-service-name"`) {
			t.Errorf("query[%d] filter does not reference configured service: %q", i, q.filter)
		}
		if !strings.Contains(q.params, "alignmentPeriod=3600s") {
			t.Errorf("query[%d] params missing alignment: %q", i, q.params)
		}
	}
}
