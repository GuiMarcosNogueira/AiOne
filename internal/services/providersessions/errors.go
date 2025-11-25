package providersessions

import "errors"

var (
	// ErrSessionNotFound indicates the requested session does not exist.
	ErrSessionNotFound = errors.New("session not found")
	// ErrUserIDRequired indicates the user id is missing.
	ErrUserIDRequired = errors.New("user id required")
	// ErrProviderRequired indicates the provider name is missing.
	ErrProviderRequired = errors.New("provider name required")
	// ErrTitleRequired indicates the session title is missing.
	ErrTitleRequired = errors.New("title required")
	// ErrSessionIDRequired indicates the session identifier is missing.
	ErrSessionIDRequired = errors.New("session id required")
)
