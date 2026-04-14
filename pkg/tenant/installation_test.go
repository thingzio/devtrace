package tenant_test

import (
	"context"
	"testing"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestSaveAndListInstallations(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99900010

	ctx := context.Background()
	tn, err := tenant.UpsertTenant(ctx, db, ghID, "inst-user", "inst@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM github_app_installation WHERE tenant_id = $1", tn.ID)
		cleanup(t, db, ghID)
	})

	err = tenant.SaveInstallation(ctx, db, tn.ID, 1001, 99, "Organization", "acme-org")
	if err != nil {
		t.Fatalf("save installation: %v", err)
	}

	list, err := tenant.ListInstallations(ctx, db, tn.ID)
	if err != nil {
		t.Fatalf("list installations: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].InstallationID != 1001 {
		t.Errorf("installation_id = %d, want 1001", list[0].InstallationID)
	}
	if list[0].TargetLogin != "acme-org" {
		t.Errorf("target_login = %q, want %q", list[0].TargetLogin, "acme-org")
	}
	if list[0].SuspendedAt != nil {
		t.Error("suspended_at should be nil")
	}
}

func TestSuspendInstallation(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99900011

	ctx := context.Background()
	tn, err := tenant.UpsertTenant(ctx, db, ghID, "susp-user", "susp@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM github_app_installation WHERE tenant_id = $1", tn.ID)
		cleanup(t, db, ghID)
	})

	err = tenant.SaveInstallation(ctx, db, tn.ID, 1002, 99, "Organization", "beta-org")
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	err = tenant.SuspendInstallation(ctx, db, 1002)
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}

	list, err := tenant.ListInstallations(ctx, db, tn.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].SuspendedAt == nil {
		t.Fatal("suspended_at should be set")
	}
}

func TestGetActiveInstallations(t *testing.T) {
	db := testDB(t)
	const ghID int64 = 99900012

	ctx := context.Background()
	tn, err := tenant.UpsertTenant(ctx, db, ghID, "active-user", "active@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM github_app_installation WHERE tenant_id = $1", tn.ID)
		cleanup(t, db, ghID)
	})

	err = tenant.SaveInstallation(ctx, db, tn.ID, 2001, 99, "Organization", "org-a")
	if err != nil {
		t.Fatalf("save org-a: %v", err)
	}
	err = tenant.SaveInstallation(ctx, db, tn.ID, 2002, 99, "User", "user-b")
	if err != nil {
		t.Fatalf("save user-b: %v", err)
	}

	err = tenant.SuspendInstallation(ctx, db, 2002)
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}

	active, err := tenant.GetActiveInstallations(ctx, db, tn.ID)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("len = %d, want 1", len(active))
	}
	if active[0].InstallationID != 2001 {
		t.Errorf("installation_id = %d, want 2001", active[0].InstallationID)
	}
	if active[0].TargetLogin != "org-a" {
		t.Errorf("target_login = %q, want %q", active[0].TargetLogin, "org-a")
	}
}
