package github

import (
	"testing"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func TestInstallationClientImplementsInterface(t *testing.T) {
	var _ Client = &InstallationClient{}
}

func TestNewInstallationClient(t *testing.T) {
	cfg := &tenant.GitHubAppConfig{AppID: 12345}
	c := NewInstallationClient(cfg, 99)

	if c.appCfg.AppID != 12345 {
		t.Errorf("want AppID 12345, got %d", c.appCfg.AppID)
	}
	if c.installationID != 99 {
		t.Errorf("want installationID 99, got %d", c.installationID)
	}
	if c.token != "" {
		t.Error("expected empty initial token")
	}
	if !c.expiresAt.IsZero() {
		t.Error("expected zero initial expiresAt")
	}
}
