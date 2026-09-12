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

package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/thingzio/devtrace/pkg/data/postgres"
)

func testStore(t *testing.T) *postgres.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable"
	}
	store, err := postgres.New(context.Background(), dsn, postgres.DefaultPoolConfig())
	if err != nil {
		t.Skipf("skipping integration test: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestMigrate(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Run again to verify idempotency
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate (idempotent): %v", err)
	}
}
