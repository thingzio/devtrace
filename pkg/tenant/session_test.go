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

package tenant_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestCreateAndValidateSession(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99910001

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "sess-alice", "sess-alice@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rawToken, err := tenant.CreateSession(ctx, db, created.ID, 10*time.Minute)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if rawToken == "" {
		t.Fatal("raw token is empty")
	}

	got, err := tenant.ValidateSession(ctx, db, rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("tenant id = %s, want %s", got.ID, created.ID)
	}
	if got.Username != "sess-alice" {
		t.Fatalf("username = %q, want %q", got.Username, "sess-alice")
	}
}

func TestValidateExpiredSession(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99910002

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "sess-bob", "sess-bob@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rawToken, err := tenant.CreateSession(ctx, db, created.ID, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	_, err = tenant.ValidateSession(ctx, db, rawToken)
	if !errors.Is(err, tenant.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
}

func TestDestroySession(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99910003

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "sess-carol", "sess-carol@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rawToken, err := tenant.CreateSession(ctx, db, created.ID, 10*time.Minute)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	err = tenant.DestroySession(ctx, db, rawToken)
	if err != nil {
		t.Fatalf("destroy session: %v", err)
	}

	_, err = tenant.ValidateSession(ctx, db, rawToken)
	if !errors.Is(err, tenant.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
}

func TestGetLastSignIns(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	ids := []int64{99910010, 99910011}
	t.Cleanup(func() {
		for _, id := range ids {
			cleanup(t, db, id)
		}
	})

	// Create two tenants.
	tn1, err := tenant.UpsertTenant(ctx, db, ids[0], "signin-alice", "sa@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	tn2, err := tenant.UpsertTenant(ctx, db, ids[1], "signin-bob", "sb@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	// Create a session only for tn1.
	_, err = tenant.CreateSession(ctx, db, tn1.ID, 10*time.Minute)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := tenant.GetLastSignIns(ctx, db, []string{tn1.ID, tn2.ID})
	if err != nil {
		t.Fatalf("GetLastSignIns: %v", err)
	}

	// tn1 should have a sign-in time.
	if result[tn1.ID] == nil {
		t.Error("expected sign-in time for tn1")
	}

	// tn2 should not (no sessions).
	if result[tn2.ID] != nil {
		t.Errorf("expected nil sign-in for tn2, got %v", result[tn2.ID])
	}
}

func TestGetLastSignIns_Empty(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	result, err := tenant.GetLastSignIns(ctx, db, nil)
	if err != nil {
		t.Fatalf("GetLastSignIns(nil): %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for empty input, got %v", result)
	}

	result, err = tenant.GetLastSignIns(ctx, db, []string{})
	if err != nil {
		t.Fatalf("GetLastSignIns(empty): %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for empty slice, got %v", result)
	}
}

func TestHashToken(t *testing.T) {
	const input = "test-token-value"
	want := sha256.Sum256([]byte(input))
	wantHex := hex.EncodeToString(want[:])

	got := tenant.HashToken(input)
	if got != wantHex {
		t.Fatalf("HashToken(%q) = %q, want %q", input, got, wantHex)
	}

	// Verify deterministic.
	if tenant.HashToken(input) != got {
		t.Fatal("HashToken is not deterministic")
	}
}
