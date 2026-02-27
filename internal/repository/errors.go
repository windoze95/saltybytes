package repository

// NotFoundError is an error type for when a resource is not found.
type NotFoundError struct {
	message string
}

// Error returns the error message.
func (e NotFoundError) Error() string {
	return e.message
}
