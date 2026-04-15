package tenant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var ErrSessionInvalid = errors.New("session expired or not found")

// CreateSession generates a random 256-bit token, stores its SHA-256 hash
// in the database, and returns the raw token for the cookie.
func CreateSession(ctx context.Context, db *sql.DB, tenantID string, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	rawToken := hex.EncodeToString(raw)
	hashed := HashToken(rawToken)

	_, err := db.ExecContext(ctx,
		`INSERT INTO devtrace_session (id, tenant_id, expires_at) VALUES ($1, $2, NOW() + $3::interval)`,
		hashed, tenantID, ttl.String())
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return rawToken, nil
}

// ValidateSession checks the session token and returns the associated tenant.
func ValidateSession(ctx context.Context, db *sql.DB, rawToken string) (*Tenant, error) {
	hashed := HashToken(rawToken)
	row := db.QueryRowContext(ctx, `
		SELECT t.id, t.github_id, t.username, COALESCE(t.email,''), COALESCE(t.avatar_url,''),
		       COALESCE(t.name,''), COALESCE(t.company,''), COALESCE(t.location,''), COALESCE(t.bio,''),
		       t.plan, t.status, t.max_contributors, t.tos_accepted_at, t.created_at, t.updated_at
		FROM devtrace_session s
		JOIN devtrace_tenant t ON t.id = s.tenant_id
		WHERE s.id = $1 AND s.expires_at > NOW()`, hashed)

	t, err := scanTenant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("validating session: %w", err)
	}
	return t, nil
}

// DestroySession removes a session by its raw token.
func DestroySession(ctx context.Context, db *sql.DB, rawToken string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM devtrace_session WHERE id = $1`, HashToken(rawToken))
	if err != nil {
		return fmt.Errorf("destroying session: %w", err)
	}
	return nil
}

// GetLastSignIn returns the most recent session creation time for a tenant, or nil.
func GetLastSignIn(ctx context.Context, db *sql.DB, tenantID string) *time.Time {
	var t sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT MAX(created_at) FROM devtrace_session WHERE tenant_id = $1`, tenantID).Scan(&t); err != nil || !t.Valid {
		return nil
	}
	return &t.Time
}

// HashToken returns the hex-encoded SHA-256 hash of a raw token.
func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
