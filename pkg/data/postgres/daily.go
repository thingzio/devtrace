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

package postgres

import (
	"context"
	"fmt"
	"time"
)

// DailyCount holds a single day's count for chart rendering.
type DailyCount struct {
	Day   time.Time
	Count int
}

// dailyCounts runs a query that returns (day, count) rows for the last N days.
func (s *Store) dailyCounts(ctx context.Context, query string, days int, label string) ([]DailyCount, error) {
	rows, err := s.db.QueryContext(ctx, query, days)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	defer rows.Close()

	var result []DailyCount
	for rows.Next() {
		var d DailyCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			return nil, fmt.Errorf("%s scan: %w", label, err)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// HourlyCount is an alias for DailyCount used for hourly chart rendering.
type HourlyCount = DailyCount
