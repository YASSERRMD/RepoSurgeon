package queue

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// queryTimeout bounds every queue statement.
const queryTimeout = 5 * time.Second

// queryExecutor is the narrow database surface the queue needs.
type queryExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func isNoRows(err error) bool {
	return err == pgx.ErrNoRows
}
