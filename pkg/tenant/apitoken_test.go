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
	"errors"
	"strings"
	"testing"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestCreateAndValidateAPIToken(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99920001

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "tok-alice", "tok-alice@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rawToken, err := tenant.CreateAPIToken(ctx, db, created.ID, "ci-token")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if !strings.HasPrefix(rawToken, "dt_") {
		t.Fatalf("token prefix = %q, want dt_", rawToken[:3])
	}
	// dt_ + 64 hex chars = 67
	if len(rawToken) != 67 {
		t.Fatalf("token length = %d, want 67", len(rawToken))
	}

	got, err := tenant.ValidateAPIToken(ctx, db, rawToken)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("tenant id = %s, want %s", got.ID, created.ID)
	}
	if got.Username != "tok-alice" {
		t.Fatalf("username = %q, want %q", got.Username, "tok-alice")
	}
}

func TestValidateInvalidToken(t *testing.T) {
	db := testDB(t)

	_, err := tenant.ValidateAPIToken(context.Background(), db, "dt_bogus_garbage_token")
	if !errors.Is(err, tenant.ErrTokenInvalid) {
		t.Fatalf("err = %v, want ErrTokenInvalid", err)
	}
}

func TestListAPITokens(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99920002

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "tok-bob", "tok-bob@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	_, errT1 := tenant.CreateAPIToken(ctx, db, created.ID, "deploy")
	if errT1 != nil {
		t.Fatalf("create token 1: %v", errT1)
	}
	_, errT2 := tenant.CreateAPIToken(ctx, db, created.ID, "cli")
	if errT2 != nil {
		t.Fatalf("create token 2: %v", errT2)
	}

	tokens, err := tenant.ListAPITokens(ctx, db, created.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("count = %d, want 2", len(tokens))
	}
	// Ordered by created_at DESC, so "cli" is first.
	if tokens[0].Name != "cli" {
		t.Fatalf("first token name = %q, want %q", tokens[0].Name, "cli")
	}
	if tokens[1].Name != "deploy" {
		t.Fatalf("second token name = %q, want %q", tokens[1].Name, "deploy")
	}
}

func TestRevokeAPIToken(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99920003

	t.Cleanup(func() { cleanup(t, db, ghID) })

	ctx := context.Background()

	created, err := tenant.UpsertTenant(ctx, db, ghID, "tok-carol", "tok-carol@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rawToken, err := tenant.CreateAPIToken(ctx, db, created.ID, "revoke-me")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// List to get the token ID.
	tokens, err := tenant.ListAPITokens(ctx, db, created.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("count = %d, want 1", len(tokens))
	}

	err = tenant.RevokeAPIToken(ctx, db, created.ID, tokens[0].ID)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}

	_, err = tenant.ValidateAPIToken(ctx, db, rawToken)
	if !errors.Is(err, tenant.ErrTokenInvalid) {
		t.Fatalf("err = %v, want ErrTokenInvalid", err)
	}
}

func TestRevokeWrongTenant(t *testing.T) {
	db := testDB(t)
	const ghIDA int64 = 99920004
	const ghIDB int64 = 99920005

	t.Cleanup(func() {
		cleanup(t, db, ghIDA)
		cleanup(t, db, ghIDB)
	})

	ctx := context.Background()

	tenantA, err := tenant.UpsertTenant(ctx, db, ghIDA, "tok-dave", "tok-dave@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	tenantB, err := tenant.UpsertTenant(ctx, db, ghIDB, "tok-eve", "tok-eve@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert B: %v", err)
	}

	_, errTok := tenant.CreateAPIToken(ctx, db, tenantA.ID, "secret")
	if errTok != nil {
		t.Fatalf("create token: %v", errTok)
	}

	tokens, err := tenant.ListAPITokens(ctx, db, tenantA.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("count = %d, want 1", len(tokens))
	}

	// Tenant B tries to revoke tenant A's token — should fail.
	err = tenant.RevokeAPIToken(ctx, db, tenantB.ID, tokens[0].ID)
	if err == nil {
		t.Fatal("expected error revoking another tenant's token")
	}
}
