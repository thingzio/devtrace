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
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GetSyncState returns the time stored under the given key in sync_state.
// Returns the zero time if the key does not exist.
func (s *Store) GetSyncState(ctx context.Context, key string) (time.Time, error) {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM devtrace_sync_state WHERE key = $1`, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("get sync state %q: %w", key, err)
	}

	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse sync state %q: %w", val, err)
	}
	return t, nil
}

// SaveSyncState upserts the given time value under key in sync_state.
func (s *Store) SaveSyncState(ctx context.Context, key string, val time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devtrace_sync_state (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, val.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save sync state %q: %w", key, err)
	}
	return nil
}
