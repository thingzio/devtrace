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

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/thingzio/devtrace/pkg/config"
)

type Store struct {
	db *sql.DB

	// bg tracks fire-and-forget goroutines (e.g., persisting score
	// results outside the request lifecycle). srv.Shutdown waits for
	// handlers to RETURN, not for goroutines they spawned — without
	// tracking, an in-flight write could hit the pool after Close().
	// Use Store.Go(...) to spawn and Store.WaitBackground(ctx) to drain
	// before close.
	bg sync.WaitGroup
}

type PoolConfig struct {
	AppName         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		AppName:         "devtrace-site",
		MaxOpenConns:    config.GetEnvAsInt("DB_MAX_OPEN_CONNS", 10),
		MaxIdleConns:    config.GetEnvAsInt("DB_MAX_IDLE_CONNS", 5),
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
}

// applyAppName appends application_name to a DSN if not already present.
func applyAppName(dsn, appName string) string {
	if appName == "" || strings.Contains(dsn, "application_name") {
		return dsn
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if strings.Contains(dsn, "?") {
			return dsn + "&application_name=" + appName
		}
		return dsn + "?application_name=" + appName
	}
	return dsn + " application_name=" + appName
}

func New(ctx context.Context, dsn string, cfg PoolConfig) (*Store, error) {
	db, err := sql.Open("postgres", applyAppName(dsn, cfg.AppName))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return &Store{db: db}, nil
}

func NewFromEnv(ctx context.Context) (*Store, error) {
	dsn := config.GetEnv("DATABASE_URL", "postgres://devtrace:devtrace@localhost:5432/devtrace?sslmode=disable")
	return New(ctx, dsn, DefaultPoolConfig())
}

func (s *Store) DB() *sql.DB {
	return s.db
}

// Go spawns fn in a tracked goroutine. Use this for fire-and-forget DB
// writes that must outlive the request context (e.g., persisting a score
// after the API response has been sent). Pair with WaitBackground in the
// shutdown path so writes drain before Close runs.
func (s *Store) Go(fn func()) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("background goroutine panicked", "panic", rec)
			}
		}()
		fn()
	}()
}

// WaitBackground blocks until all tracked goroutines exit or ctx is
// canceled. Returns ctx.Err() on timeout so callers can log the leak.
func (s *Store) WaitBackground(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) Close() error {
	return s.db.Close()
}
