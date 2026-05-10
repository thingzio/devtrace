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
		_, _ = db.ExecContext(ctx, "DELETE FROM devtrace_app_installation WHERE tenant_id = $1", tn.ID)
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
		_, _ = db.ExecContext(ctx, "DELETE FROM devtrace_app_installation WHERE tenant_id = $1", tn.ID)
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
		_, _ = db.ExecContext(ctx, "DELETE FROM devtrace_app_installation WHERE tenant_id = $1", tn.ID)
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

func TestListTenantsWithoutInstall(t *testing.T) {
	db := testDB(t)

	ctx := context.Background()
	const ghIDWithInstall int64 = 99900020
	const ghIDNoInstall int64 = 99900021

	tnWith, err := tenant.UpsertTenant(ctx, db, ghIDWithInstall, "has-install", "hi@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert tenant with install: %v", err)
	}
	tnWithout, err := tenant.UpsertTenant(ctx, db, ghIDNoInstall, "no-install", "ni@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert tenant without install: %v", err)
	}
	_ = tnWithout // used for cleanup

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM devtrace_app_installation WHERE tenant_id = $1", tnWith.ID)
		cleanup(t, db, ghIDWithInstall)
		cleanup(t, db, ghIDNoInstall)
	})

	// Add installation for one tenant only.
	err = tenant.SaveInstallation(ctx, db, tnWith.ID, 3001, 99, "Organization", "org-with")
	if err != nil {
		t.Fatalf("save installation: %v", err)
	}

	results, err := tenant.ListTenantsWithoutInstall(ctx, db)
	if err != nil {
		t.Fatalf("list tenants without install: %v", err)
	}

	// Tenant without installation should appear in results.
	found := false
	for _, r := range results {
		if r.Username == "no-install" {
			found = true
			if r.Plan != "pro" {
				t.Errorf("plan = %q, want %q", r.Plan, "pro")
			}
		}
		if r.Username == "has-install" {
			t.Error("tenant with active installation should not appear")
		}
	}
	if !found {
		t.Error("tenant without installation not found in results")
	}
}

func TestListTenantsWithoutInstall_SuspendedDoesNotCount(t *testing.T) {
	db := testDB(t)

	ctx := context.Background()
	const ghID int64 = 99900022

	tn, err := tenant.UpsertTenant(ctx, db, ghID, "suspended-install", "si@test.com", "", "", "", "", "")
	if err != nil {
		t.Fatalf("upsert tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM devtrace_app_installation WHERE tenant_id = $1", tn.ID)
		cleanup(t, db, ghID)
	})

	// Add then suspend installation.
	err = tenant.SaveInstallation(ctx, db, tn.ID, 3002, 99, "Organization", "org-susp")
	if err != nil {
		t.Fatalf("save installation: %v", err)
	}
	err = tenant.SuspendInstallation(ctx, db, 3002)
	if err != nil {
		t.Fatalf("suspend installation: %v", err)
	}

	results, err := tenant.ListTenantsWithoutInstall(ctx, db)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	// Suspended installation should not count — tenant should appear in results.
	found := false
	for _, r := range results {
		if r.Username == "suspended-install" {
			found = true
		}
	}
	if !found {
		t.Error("tenant with only suspended installations should appear in no-install list")
	}
}
