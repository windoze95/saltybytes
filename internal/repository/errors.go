package repository

import "errors"

// Sentinel errors for unique-constraint violations on user creation. Handlers
// match these with errors.Is to return meaningful conflict responses without
// leaking raw database errors.
var (
	ErrUsernameTaken = errors.New("username already in use")
	ErrEmailTaken    = errors.New("email already in use")
)

// NotFoundError is an error type for when a resource is not found.
type NotFoundError struct {
	message string
}

// Error returns the error message.
func (e NotFoundError) Error() string {
	return e.message
}
