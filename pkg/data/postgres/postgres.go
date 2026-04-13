package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	"github.com/thingzio/devtrace/pkg/config"
)

type Store struct {
	db *sql.DB
}

type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    config.GetEnvAsInt("DB_MAX_OPEN_CONNS", 10),
		MaxIdleConns:    config.GetEnvAsInt("DB_MAX_IDLE_CONNS", 5),
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
}

func New(ctx context.Context, dsn string, cfg PoolConfig) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
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

func (s *Store) Close() error {
	return s.db.Close()
}
