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

package tenant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const tokenPrefix = "dt_"

var ErrTokenInvalid = errors.New("invalid or revoked API token")

type APITokenInfo struct {
	ID        string
	Name      string
	LastUsed  *time.Time
	CreatedAt time.Time
}

// CreateAPIToken generates a prefixed API token, stores its hash, and returns
// the raw token (shown once to the user, never stored).
func CreateAPIToken(ctx context.Context, db *sql.DB, tenantID, name string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating api token: %w", err)
	}
	rawToken := tokenPrefix + hex.EncodeToString(raw)
	hashed := HashToken(rawToken)

	var id string
	err := db.QueryRowContext(ctx,
		`INSERT INTO devtrace_api_token (tenant_id, name, token_hash) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, name, hashed).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("creating api token: %w", err)
	}
	return rawToken, nil
}

// ValidateAPIToken hashes the raw token, looks it up, and returns the
// owning tenant. The last_used_at update is folded into the same CTE
// query so validation is a single round-trip — previously the update
// shipped as a fire-and-forget goroutine that could outlive the request
// and race the connection pool on shutdown.
func ValidateAPIToken(ctx context.Context, db *sql.DB, rawToken string) (*Tenant, error) {
	hashed := HashToken(rawToken)

	row := db.QueryRowContext(ctx, `
		WITH used AS (
			UPDATE devtrace_api_token SET last_used_at = NOW()
			WHERE token_hash = $1
			RETURNING tenant_id
		)
		SELECT t.id, t.github_id, t.username, COALESCE(t.email,''), COALESCE(t.avatar_url,''),
		       COALESCE(t.name,''), COALESCE(t.company,''), COALESCE(t.location,''), COALESCE(t.bio,''),
		       t.plan, t.status, t.max_contributors, t.tos_accepted_at, t.created_at, t.updated_at
		FROM used u
		JOIN devtrace_tenant t ON t.id = u.tenant_id`, hashed)

	t, err := scanTenant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("validating api token: %w", err)
	}
	return t, nil
}

// ListAPITokens returns all tokens for a tenant, ordered by creation time descending.
func ListAPITokens(ctx context.Context, db *sql.DB, tenantID string) ([]APITokenInfo, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, last_used_at, created_at
		 FROM devtrace_api_token WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing api tokens: %w", err)
	}
	defer rows.Close()

	var tokens []APITokenInfo
	for rows.Next() {
		var ti APITokenInfo
		var lastUsed sql.NullTime
		if err := rows.Scan(&ti.ID, &ti.Name, &lastUsed, &ti.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning api token: %w", err)
		}
		if lastUsed.Valid {
			ti.LastUsed = &lastUsed.Time
		}
		tokens = append(tokens, ti)
	}
	return tokens, rows.Err()
}

// RevokeAPIToken deletes a token owned by the given tenant.
func RevokeAPIToken(ctx context.Context, db *sql.DB, tenantID, tokenID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM devtrace_api_token WHERE id = $1 AND tenant_id = $2`, tokenID, tenantID)
	if err != nil {
		return fmt.Errorf("revoking api token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("token not found or not owned by tenant")
	}
	return nil
}
