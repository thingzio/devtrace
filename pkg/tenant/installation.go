package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Installation represents a GitHub App installation linked to a tenant.
type Installation struct {
	ID             string
	TenantID       string
	InstallationID int64
	TargetType     string
	TargetLogin    string
	SuspendedAt    *time.Time
	CreatedAt      time.Time
}

// ActiveInstallation is a non-suspended installation summary.
type ActiveInstallation struct {
	InstallationID int64
	TargetLogin    string
}

// SaveInstallation upserts a GitHub App installation for a tenant.
func SaveInstallation(ctx context.Context, db *sql.DB, tenantID string, installationID, appID int64, targetType, targetLogin string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO devtrace_app_installation (tenant_id, installation_id, app_id, target_type, target_login)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (installation_id) DO UPDATE SET
		   tenant_id = $1, app_id = $3, target_type = $4, target_login = $5, suspended_at = NULL`,
		tenantID, installationID, appID, targetType, targetLogin)
	if err != nil {
		return fmt.Errorf("saving installation: %w", err)
	}
	return nil
}

// SuspendInstallation marks an installation as suspended.
func SuspendInstallation(ctx context.Context, db *sql.DB, installationID int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE devtrace_app_installation SET suspended_at = NOW() WHERE installation_id = $1`,
		installationID)
	if err != nil {
		return fmt.Errorf("suspending installation: %w", err)
	}
	return nil
}

// ListInstallations returns all installations for a tenant.
func ListInstallations(ctx context.Context, db *sql.DB, tenantID string) ([]Installation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, tenant_id, installation_id, target_type, target_login, suspended_at, created_at
		 FROM devtrace_app_installation WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing installations: %w", err)
	}
	defer rows.Close()

	var result []Installation
	for rows.Next() {
		var inst Installation
		var suspended sql.NullTime
		if err := rows.Scan(&inst.ID, &inst.TenantID, &inst.InstallationID, &inst.TargetType, &inst.TargetLogin, &suspended, &inst.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning installation: %w", err)
		}
		if suspended.Valid {
			inst.SuspendedAt = &suspended.Time
		}
		result = append(result, inst)
	}
	return result, rows.Err()
}

// GetAllActiveInstallations returns non-suspended installations across all tenants
// for the given app ID. Used at startup to build the GitHub API token pool.
func GetAllActiveInstallations(ctx context.Context, db *sql.DB, appID int64) ([]ActiveInstallation, error) {
	return queryActiveInstallations(ctx, db,
		`SELECT DISTINCT installation_id, target_login FROM devtrace_app_installation
		 WHERE suspended_at IS NULL AND app_id = $1`, appID)
}

// GetActiveInstallations returns non-suspended installations for a tenant.
func GetActiveInstallations(ctx context.Context, db *sql.DB, tenantID string) ([]ActiveInstallation, error) {
	return queryActiveInstallations(ctx, db,
		`SELECT installation_id, target_login FROM devtrace_app_installation
		 WHERE tenant_id = $1 AND suspended_at IS NULL`, tenantID)
}

func queryActiveInstallations(ctx context.Context, db *sql.DB, query string, args ...any) ([]ActiveInstallation, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query active installations: %w", err)
	}
	defer rows.Close()

	var result []ActiveInstallation
	for rows.Next() {
		var a ActiveInstallation
		if err := rows.Scan(&a.InstallationID, &a.TargetLogin); err != nil {
			return nil, fmt.Errorf("scanning active installation: %w", err)
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
