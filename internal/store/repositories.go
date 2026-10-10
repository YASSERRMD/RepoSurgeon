package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL SQLSTATE codes the repositories translate into typed domain
// errors so callers never have to parse driver messages.
const (
	sqlStateUniqueViolation = "23505"
	sqlStateForeignKey      = "23503"
	sqlStateCheckViolation  = "23514"
)

// rollbackTx discards a transaction. After a successful Commit it returns
// pgx.ErrTxClosed, which is expected and therefore ignored.
func rollbackTx(ctx context.Context, tx pgx.Tx) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		// Rollback failures after a failed statement are non-actionable here;
		// the original error is the one worth surfacing.
		_ = err
	}
}

// isUniqueViolation reports whether err is a duplicate-key error.
func isUniqueViolation(err error) bool {
	return hasSQLState(err, sqlStateUniqueViolation)
}

// isForeignKeyViolation reports whether err broke a foreign key.
func isForeignKeyViolation(err error) bool {
	return hasSQLState(err, sqlStateForeignKey)
}

// isCheckViolation reports whether err broke a CHECK constraint, such as an
// unrecognized role or status value.
func isCheckViolation(err error) bool {
	return hasSQLState(err, sqlStateCheckViolation)
}

func hasSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// nilIfEmpty maps an empty string to SQL NULL, used for the nullable
// foreign-key columns shared by audit events.
func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// derefString maps a scanned SQL NULL back to the empty string.
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// likeEscape escapes the ILIKE wildcards in a user-supplied filter so it can
// only ever match a literal substring.
func likeEscape(filter string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return replacer.Replace(filter)
}

// nonEmpty reports whether every value is non-empty.
func nonEmpty(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

// validationErrorf builds an ErrValidation carrying a field-level reason, which
// is the shape the API layer returns to clients.
func validationErrorf(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrValidation, field, fmt.Sprintf(format, args...))
}

// roleCanWrite reports whether a membership role may mutate projects. Owner,
// admin and member are write roles; viewer is read-only.
func roleCanWrite(role string) bool {
	switch role {
	case domain.RoleOwner, domain.RoleAdmin, domain.RoleMember:
		return true
	default:
		return false
	}
}

// isKnownRole reports whether role is one of the four membership roles.
func isKnownRole(role string) bool {
	switch role {
	case domain.RoleOwner, domain.RoleAdmin, domain.RoleMember, domain.RoleViewer:
		return true
	default:
		return false
	}
}
