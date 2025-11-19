package users

import (
	"context"
	"errors"
)

// Repository defines persistence operations for users.
type Repository interface {
	Create(ctx context.Context, params CreateParams) (Aggregate, error)
	GetByEmail(ctx context.Context, email string) (Aggregate, error)
	GetByID(ctx context.Context, id string) (Aggregate, error)
	UpdateLastLogin(ctx context.Context, id string) error
}

var (
	// ErrUserExists indicates a duplicate e-mail attempt.
	ErrUserExists = errors.New("user already exists")
	// ErrUserNotFound is returned when the user is missing.
	ErrUserNotFound = errors.New("user not found")
)
