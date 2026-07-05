package repository

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// The pgx driver (gorm.io/driver/postgres) surfaces unique violations as
// *pgconn.PgError. The old code type-asserted *pq.Error, which never
// matched, so duplicate signups surfaced as opaque 500s.

func TestMapUserUniqueViolation_Username(t *testing.T) {
	err := mapUserUniqueViolation(&pgconn.PgError{Code: "23505", ConstraintName: "uni_users_username"})
	if !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("err = %v, want ErrUsernameTaken", err)
	}
}

func TestMapUserUniqueViolation_Email(t *testing.T) {
	err := mapUserUniqueViolation(&pgconn.PgError{Code: "23505", ConstraintName: "uni_users_email"})
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("err = %v, want ErrEmailTaken", err)
	}
}

func TestMapUserUniqueViolation_FallsBackToMessage(t *testing.T) {
	// Older schemas name the constraint differently; the message still
	// carries the column.
	err := mapUserUniqueViolation(&pgconn.PgError{
		Code:    "23505",
		Message: `duplicate key value violates unique constraint "users_email_key"`,
	})
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("err = %v, want ErrEmailTaken", err)
	}
}

func TestMapUserUniqueViolation_WrappedError(t *testing.T) {
	wrapped := fmt.Errorf("create user: %w", &pgconn.PgError{Code: "23505", ConstraintName: "uni_users_username"})
	if !errors.Is(mapUserUniqueViolation(wrapped), ErrUsernameTaken) {
		t.Error("wrapped pgconn errors should still map")
	}
}

func TestMapUserUniqueViolation_PassthroughOtherErrors(t *testing.T) {
	sentinel := errors.New("some other failure")
	if got := mapUserUniqueViolation(sentinel); got != sentinel {
		t.Errorf("non-unique-violation errors must pass through unchanged, got %v", got)
	}

	notUnique := &pgconn.PgError{Code: "23503", ConstraintName: "fk_users_something"}
	if got := mapUserUniqueViolation(notUnique); !errors.As(got, new(*pgconn.PgError)) {
		t.Errorf("non-23505 pg errors must pass through, got %v", got)
	}
}
