package providersessions

import "errors"

var (
	// ErrSessionNotFound indicates the requested session does not exist.
	ErrSessionNotFound = errors.New("provider session not found")
)
