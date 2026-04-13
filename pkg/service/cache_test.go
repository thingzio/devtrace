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
