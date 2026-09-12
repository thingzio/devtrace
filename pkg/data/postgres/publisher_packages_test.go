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

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/model"
)

func TestPublisherProfileRoundTrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "publisher-rt-test"
		registry = "npm"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_publisher_packages
			 WHERE provider=$1 AND username=$2 AND registry=$3`,
			provider, user, registry)
	})
	_, _ = store.DB().ExecContext(ctx,
		`DELETE FROM devtrace_publisher_packages
		 WHERE provider=$1 AND username=$2 AND registry=$3`,
		provider, user, registry)

	got, ts, err := store.GetPublisherProfile(ctx, provider, user, registry)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if got != nil || !ts.IsZero() {
		t.Fatalf("empty: expected nil/zero, got %+v / %v", got, ts)
	}

	profile := &model.RegistryProfile{
		PackageCount: 42,
		Top: []model.Package{
			{Name: "alpha", Role: "write", URL: "https://www.npmjs.com/package/alpha"},
			{Name: "beta", Role: "write", URL: "https://www.npmjs.com/package/beta"},
		},
	}
	if serr := store.SavePublisherProfile(ctx, provider, user, registry, profile); serr != nil {
		t.Fatalf("save: %v", serr)
	}

	got, ts, err = store.GetPublisherProfile(ctx, provider, user, registry)
	if err != nil {
		t.Fatalf("get after save: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil after save")
	}
	if got.PackageCount != 42 {
		t.Errorf("PackageCount: got %d, want 42", got.PackageCount)
	}
	if len(got.Top) != 2 {
		t.Fatalf("Top len: got %d, want 2", len(got.Top))
	}
	if got.Top[0].Name != "alpha" || got.Top[0].URL == "" {
		t.Errorf("Top[0]: %+v", got.Top[0])
	}
	if ts.IsZero() || time.Since(ts) > time.Minute {
		t.Errorf("fetched_at: got %v", ts)
	}
}

func TestPublisherProfileSentinelOnEmpty(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "publisher-empty-test"
		registry = "npm"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_publisher_packages
			 WHERE provider=$1 AND username=$2 AND registry=$3`,
			provider, user, registry)
	})

	// Sentinel: zero count, no packages. Records the fetch attempt so
	// service-layer TTL check suppresses re-asking the registry for
	// users who don't publish anything.
	if err := store.SavePublisherProfile(ctx, provider, user, registry,
		&model.RegistryProfile{}); err != nil {
		t.Fatalf("save sentinel: %v", err)
	}

	got, ts, err := store.GetPublisherProfile(ctx, provider, user, registry)
	if err != nil {
		t.Fatalf("get sentinel: %v", err)
	}
	if got == nil {
		t.Fatal("sentinel should round-trip a non-nil profile")
	}
	if got.PackageCount != 0 || len(got.Top) != 0 {
		t.Errorf("sentinel shape: count=%d top=%d", got.PackageCount, len(got.Top))
	}
	if ts.IsZero() {
		t.Error("fetched_at must be non-zero on sentinel")
	}
}

func TestPublisherProfileUpsertReplaces(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "publisher-replace-test"
		registry = "npm"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_publisher_packages
			 WHERE provider=$1 AND username=$2 AND registry=$3`,
			provider, user, registry)
	})

	first := &model.RegistryProfile{
		PackageCount: 3,
		Top: []model.Package{
			{Name: "old-pkg", Role: "write"},
		},
	}
	if err := store.SavePublisherProfile(ctx, provider, user, registry, first); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second := &model.RegistryProfile{
		PackageCount: 7,
		Top: []model.Package{
			{Name: "new-pkg-a", Role: "write"},
			{Name: "new-pkg-b", Role: "write"},
		},
	}
	if err := store.SavePublisherProfile(ctx, provider, user, registry, second); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, _, err := store.GetPublisherProfile(ctx, provider, user, registry)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PackageCount != 7 || len(got.Top) != 2 || got.Top[0].Name != "new-pkg-a" {
		t.Errorf("upsert should replace, got %+v", got)
	}
}

func TestPublisherProfileMultipleRegistries(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const (
		provider = "github"
		user     = "publisher-multi-test"
	)
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(ctx,
			`DELETE FROM devtrace_publisher_packages WHERE provider=$1 AND username=$2`,
			provider, user)
	})

	if err := store.SavePublisherProfile(ctx, provider, user, "npm",
		&model.RegistryProfile{PackageCount: 5, Top: []model.Package{{Name: "n"}}}); err != nil {
		t.Fatalf("save npm: %v", err)
	}
	if err := store.SavePublisherProfile(ctx, provider, user, "pypi",
		&model.RegistryProfile{PackageCount: 2, Top: []model.Package{{Name: "p"}}}); err != nil {
		t.Fatalf("save pypi: %v", err)
	}

	npm, _, err := store.GetPublisherProfile(ctx, provider, user, "npm")
	if err != nil || npm == nil {
		t.Fatalf("get npm: %v / %v", npm, err)
	}
	pypi, _, err := store.GetPublisherProfile(ctx, provider, user, "pypi")
	if err != nil || pypi == nil {
		t.Fatalf("get pypi: %v / %v", pypi, err)
	}
	if npm.PackageCount != 5 || pypi.PackageCount != 2 {
		t.Errorf("registries should be independent: npm=%d pypi=%d",
			npm.PackageCount, pypi.PackageCount)
	}
}
