package service

import (
	"testing"
	"time"

	"github.com/thingzio/devtrace/pkg/model"
)

func TestCacheGetMiss(t *testing.T) {
	c := &scoreCache{
		entries: make(map[string]*cacheEntry),
		ttl:     5 * time.Minute,
	}
	if got := c.get("nobody", ""); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestCacheSetAndGet(t *testing.T) {
	c := &scoreCache{
		entries: make(map[string]*cacheEntry),
		ttl:     5 * time.Minute,
	}
	resp := &model.ScoreResponse{
		Username: "testuser",
		Score:    &model.Score{Grade: "A", Value: 0.95},
	}
	c.set("testuser", "", resp)

	got := c.get("testuser", "")
	if got == nil {
		t.Fatal("expected cached response")
	}
	if got.Score.Grade != "A" {
		t.Errorf("grade: got %q, want A", got.Score.Grade)
	}
}

func TestCacheKeyWithRepo(t *testing.T) {
	c := &scoreCache{
		entries: make(map[string]*cacheEntry),
		ttl:     5 * time.Minute,
	}
	resp1 := &model.ScoreResponse{Username: "u", Score: &model.Score{Value: 0.5}}
	resp2 := &model.ScoreResponse{Username: "u", Score: &model.Score{Value: 0.8}}

	c.set("u", "", resp1)
	c.set("u", "org/repo", resp2)

	// Different keys — both should be retrievable.
	got1 := c.get("u", "")
	got2 := c.get("u", "org/repo")
	if got1.Score.Value != 0.5 {
		t.Errorf("without repo: got %f, want 0.5", got1.Score.Value)
	}
	if got2.Score.Value != 0.8 {
		t.Errorf("with repo: got %f, want 0.8", got2.Score.Value)
	}
}

func TestCacheExpiry(t *testing.T) {
	c := &scoreCache{
		entries: make(map[string]*cacheEntry),
		ttl:     1 * time.Millisecond,
	}
	resp := &model.ScoreResponse{Username: "u", Score: &model.Score{Value: 0.5}}
	c.set("u", "", resp)

	time.Sleep(5 * time.Millisecond)

	if got := c.get("u", ""); got != nil {
		t.Errorf("expected nil after expiry, got %+v", got)
	}
}

func TestEnrichForPlanDeepCopy(t *testing.T) {
	full := &model.ScoreResponse{
		Username: "testuser",
		Score: &model.Score{
			Grade:      "A",
			Value:      0.95,
			Categories: map[string]float64{"activity": 0.9, "provenance": 0.8},
		},
		Profile: &model.Profile{
			Name:    "Test User",
			Company: "TestCo",
		},
		Signals: &model.Signals{
			AccountAgeDays: 500,
			Followers:      100,
		},
		AISensing: &model.AISensing{
			CoAuthoredCommits: 5,
		},
		Behavior: &model.Behavior{
			ActiveDays: 30,
		},
	}

	// Get a pro-plan copy (includes all fields).
	enriched := enrichForPlan(full, "pro")

	// Mutate every pointer field in the enriched copy.
	enriched.Score.Value = 0.0
	enriched.Score.Grade = "F"
	enriched.Score.Categories["activity"] = 0.0
	enriched.Score.Categories["new_key"] = 1.0
	enriched.Profile.Name = "MUTATED"
	enriched.Profile.Company = "MUTATED"
	enriched.Signals.Followers = 0
	enriched.Signals.AccountAgeDays = 0
	enriched.AISensing.CoAuthoredCommits = 999
	enriched.Behavior.ActiveDays = 999

	// Verify original Score is untouched.
	if full.Score.Value != 0.95 {
		t.Errorf("Score.Value mutated: got %f, want 0.95", full.Score.Value)
	}
	if full.Score.Grade != "A" {
		t.Errorf("Score.Grade mutated: got %q, want A", full.Score.Grade)
	}
	if full.Score.Categories["activity"] != 0.9 {
		t.Errorf("Score.Categories[activity] mutated: got %f, want 0.9", full.Score.Categories["activity"])
	}
	if _, ok := full.Score.Categories["new_key"]; ok {
		t.Error("Score.Categories map leaked new key into original")
	}

	// Verify original Profile is untouched.
	if full.Profile.Name != "Test User" {
		t.Errorf("Profile.Name mutated: got %q, want %q", full.Profile.Name, "Test User")
	}
	if full.Profile.Company != "TestCo" {
		t.Errorf("Profile.Company mutated: got %q, want %q", full.Profile.Company, "TestCo")
	}

	// Verify original Signals is untouched.
	if full.Signals.Followers != 100 {
		t.Errorf("Signals.Followers mutated: got %d, want 100", full.Signals.Followers)
	}
	if full.Signals.AccountAgeDays != 500 {
		t.Errorf("Signals.AccountAgeDays mutated: got %d, want 500", full.Signals.AccountAgeDays)
	}

	// Verify original AISensing is untouched.
	if full.AISensing.CoAuthoredCommits != 5 {
		t.Errorf("AISensing.CoAuthoredCommits mutated: got %d, want 5", full.AISensing.CoAuthoredCommits)
	}

	// Verify original Behavior is untouched.
	if full.Behavior.ActiveDays != 30 {
		t.Errorf("Behavior.ActiveDays mutated: got %d, want 30", full.Behavior.ActiveDays)
	}

	// Verify the enriched copy holds the mutated values (sanity check).
	if enriched.Score.Value != 0.0 {
		t.Errorf("enriched Score.Value: got %f, want 0.0", enriched.Score.Value)
	}
	if enriched.Profile.Name != "MUTATED" {
		t.Errorf("enriched Profile.Name: got %q, want MUTATED", enriched.Profile.Name)
	}
}
