package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

type Tenant struct {
	ID              string
	GitHubID        int64
	Username        string
	Email           string
	AvatarURL       string
	Name            string
	Company         string
	Location        string
	Bio             string
	Plan            string
	Status          string
	MaxContributors int
	ToSAcceptedAt   *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func UpsertTenant(ctx context.Context, db *sql.DB, githubID int64, username, email, avatarURL, name, company, location, bio string) (*Tenant, error) {
	const q = `INSERT INTO devtrace_tenant (github_id, username, email, avatar_url, name, company, location, bio)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (github_id) DO UPDATE SET
			username=$2, email=$3, avatar_url=$4, name=$5, company=$6, location=$7, bio=$8, updated_at=NOW()
		RETURNING id, github_id, username, email, avatar_url,
			COALESCE(name,''), COALESCE(company,''), COALESCE(location,''), COALESCE(bio,''),
			plan, status, max_contributors, tos_accepted_at, created_at, updated_at`
	return scanTenant(db.QueryRowContext(ctx, q, githubID, username, email, avatarURL, name, company, location, bio))
}

func GetTenantByID(ctx context.Context, db *sql.DB, id string) (*Tenant, error) {
	const q = `SELECT id, github_id, username, email, avatar_url,
		COALESCE(name,''), COALESCE(company,''), COALESCE(location,''), COALESCE(bio,''),
		plan, status, max_contributors, tos_accepted_at, created_at, updated_at
		FROM devtrace_tenant WHERE id = $1`
	return scanTenant(db.QueryRowContext(ctx, q, id))
}

func GetTenantByGitHubID(ctx context.Context, db *sql.DB, githubID int64) (*Tenant, error) {
	const q = `SELECT id, github_id, username, email, avatar_url,
		COALESCE(name,''), COALESCE(company,''), COALESCE(location,''), COALESCE(bio,''),
		plan, status, max_contributors, tos_accepted_at, created_at, updated_at
		FROM devtrace_tenant WHERE github_id = $1`
	return scanTenant(db.QueryRowContext(ctx, q, githubID))
}

// GetTenantByUsername returns the tenant with the given GitHub username.
func GetTenantByUsername(ctx context.Context, db *sql.DB, username string) (*Tenant, error) {
	const q = `SELECT id, github_id, username, email, avatar_url,
		COALESCE(name,''), COALESCE(company,''), COALESCE(location,''), COALESCE(bio,''),
		plan, status, max_contributors, tos_accepted_at, created_at, updated_at
		FROM devtrace_tenant WHERE username = $1`
	return scanTenant(db.QueryRowContext(ctx, q, username))
}

// ListTenants returns all tenants ordered by creation date.
func ListTenants(ctx context.Context, db *sql.DB) ([]*Tenant, error) {
	const q = `SELECT id, github_id, username, email, avatar_url,
		COALESCE(name,''), COALESCE(company,''), COALESCE(location,''), COALESCE(bio,''),
		plan, status, max_contributors, tos_accepted_at, created_at, updated_at
		FROM devtrace_tenant ORDER BY created_at`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []*Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tenant: %w", err)
		}
		tenants = append(tenants, t)
	}
	return tenants, rows.Err()
}

func AcceptToS(ctx context.Context, db *sql.DB, tenantID string) error {
	const q = `UPDATE devtrace_tenant SET tos_accepted_at = NOW(), updated_at = NOW() WHERE id = $1`
	res, err := db.ExecContext(ctx, q, tenantID)
	if err != nil {
		return fmt.Errorf("accept tos: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateTenantPlan updates a tenant's plan and max_contributors.
func UpdateTenantPlan(ctx context.Context, db *sql.DB, tenantID, planName string, maxContributors int) (*Tenant, error) {
	const q = `UPDATE devtrace_tenant
		SET plan = $2, max_contributors = $3, updated_at = NOW()
		WHERE id = $1
		RETURNING id, github_id, username, email, avatar_url,
			COALESCE(name,''), COALESCE(company,''), COALESCE(location,''), COALESCE(bio,''),
			plan, status, max_contributors, tos_accepted_at, created_at, updated_at`
	return scanTenant(db.QueryRowContext(ctx, q, tenantID, planName, maxContributors))
}

// UpdateTenantStatus updates a tenant's status (active or suspended).
func UpdateTenantStatus(ctx context.Context, db *sql.DB, tenantID, status string) (*Tenant, error) {
	if status != StatusActive && status != StatusSuspended {
		return nil, fmt.Errorf("invalid status: %s", status)
	}
	const q = `UPDATE devtrace_tenant
		SET status = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING id, github_id, username, email, avatar_url,
			COALESCE(name,''), COALESCE(company,''), COALESCE(location,''), COALESCE(bio,''),
			plan, status, max_contributors, tos_accepted_at, created_at, updated_at`
	return scanTenant(db.QueryRowContext(ctx, q, tenantID, status))
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanTenant(row scanner) (*Tenant, error) {
	var t Tenant
	var tosAccepted sql.NullTime
	if err := row.Scan(
		&t.ID, &t.GitHubID, &t.Username, &t.Email, &t.AvatarURL,
		&t.Name, &t.Company, &t.Location, &t.Bio,
		&t.Plan, &t.Status, &t.MaxContributors, &tosAccepted, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan tenant: %w", err)
	}
	if tosAccepted.Valid {
		t.ToSAcceptedAt = &tosAccepted.Time
	}
	return &t, nil
}
