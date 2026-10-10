// Package store owns the PostgreSQL connection pool. Business logic lives in
// the repository layer; this package only manages connections and their
// limits.
package store

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Options configures the connection pool.
type Options struct {
	MaxConns           int32
	MinConns           int32
	ConnectTimeout     time.Duration
	StatementTimeoutMS int
	ApplicationName    string
}

// DefaultOptions returns conservative pool defaults.
func DefaultOptions() Options {
	return Options{
		MaxConns:           10,
		MinConns:           1,
		ConnectTimeout:     5 * time.Second,
		StatementTimeoutMS: 15000,
		ApplicationName:    "reposurgeon",
	}
}

// Open parses dsn, builds a pool and verifies connectivity with a ping.
func Open(ctx context.Context, dsn string, opts Options) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if opts.MaxConns > 0 {
		poolCfg.MaxConns = opts.MaxConns
	}
	if opts.MinConns >= 0 {
		poolCfg.MinConns = opts.MinConns
	}
	if opts.ConnectTimeout > 0 {
		poolCfg.ConnConfig.ConnectTimeout = opts.ConnectTimeout
	}
	if opts.StatementTimeoutMS > 0 {
		poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(opts.StatementTimeoutMS)
	}
	if opts.ApplicationName != "" {
		poolCfg.ConnConfig.RuntimeParams["application_name"] = opts.ApplicationName
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, orDefault(opts.ConnectTimeout, 5*time.Second))
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func orDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}
