package users

import "time"

// User represents a platform user profile.
type User struct {
	ID          string
	Email       string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastLoginAt *time.Time
}

// Credentials stores hashed password metadata.
type Credentials struct {
	UserID       string
	PasswordHash string
	PasswordAlgo string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastRotated  time.Time
}

// Settings exposes per-user preferences.
type Settings struct {
	UserID      string
	Preferences map[string]any
	Timezone    string
	Locale      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Aggregate groups the user record, credentials and settings.
type Aggregate struct {
	User        User
	Credentials Credentials
	Settings    Settings
}

// CreateParams defines the values required to create a new user aggregate.
type CreateParams struct {
	ID           string
	Email        string
	DisplayName  string
	PasswordHash string
	PasswordAlgo string
	Preferences  map[string]any
	Timezone     string
	Locale       string
}
