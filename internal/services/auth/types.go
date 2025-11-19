package auth

import "time"

// RegisterInput captures the payload required to create a user.
type RegisterInput struct {
	Email       string         `json:"email"`
	DisplayName string         `json:"display_name"`
	Password    string         `json:"password"`
	Preferences map[string]any `json:"preferences,omitempty"`
	Timezone    string         `json:"timezone,omitempty"`
	Locale      string         `json:"locale,omitempty"`
	IP          string         `json:"-"`
	UserAgent   string         `json:"-"`
}

// LoginInput captures login attempts.
type LoginInput struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	IP        string `json:"-"`
	UserAgent string `json:"-"`
}

// RefreshInput represents refresh token rotation payload.
type RefreshInput struct {
	RefreshToken string `json:"refresh_token"`
	IP           string `json:"-"`
	UserAgent    string `json:"-"`
}

// AuthResponse standardizes token responses.
type AuthResponse struct {
	AccessToken           string        `json:"access_token"`
	AccessTokenExpiresIn  time.Duration `json:"access_expires_in"`
	RefreshToken          string        `json:"refresh_token"`
	RefreshTokenExpiresIn time.Duration `json:"refresh_expires_in"`
}

// Claims stores custom JWT claims extracted by middleware.
type Claims struct {
	UserID string
	Email  string
}
