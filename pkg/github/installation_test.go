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
