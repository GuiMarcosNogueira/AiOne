package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresRepository persists users in PostgreSQL.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository builds a PostgreSQL-backed repository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) Create(ctx context.Context, params CreateParams) (Aggregate, error) {
	if r == nil || r.db == nil {
		return Aggregate{}, errors.New("users repository not initialized")
	}
	if params.Preferences == nil {
		params.Preferences = map[string]any{}
	}
	prefs, _ := json.Marshal(params.Preferences)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Aggregate{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	userQuery := `INSERT INTO users (id, email, display_name) VALUES ($1,$2,$3)
		RETURNING created_at, updated_at`
	var createdAt, updatedAt time.Time
	email := strings.ToLower(strings.TrimSpace(params.Email))
	if err := tx.QueryRowContext(ctx, userQuery, params.ID, email, params.DisplayName).
		Scan(&createdAt, &updatedAt); err != nil {
		if isUniqueViolation(err) {
			return Aggregate{}, ErrUserExists
		}
		return Aggregate{}, fmt.Errorf("insert user: %w", err)
	}

	credQuery := `INSERT INTO user_credentials (user_id, password_hash, password_algo)
		VALUES ($1,$2,$3)`
	if _, err := tx.ExecContext(ctx, credQuery, params.ID, params.PasswordHash, params.PasswordAlgo); err != nil {
		return Aggregate{}, fmt.Errorf("insert credentials: %w", err)
	}

	settingsQuery := `INSERT INTO user_settings (user_id, preferences, timezone, locale)
		VALUES ($1,$2,$3,$4)`
	if _, err := tx.ExecContext(ctx, settingsQuery, params.ID, prefs, params.Timezone, params.Locale); err != nil {
		return Aggregate{}, fmt.Errorf("insert settings: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Aggregate{}, fmt.Errorf("commit: %w", err)
	}

	return Aggregate{
		User:        User{ID: params.ID, Email: email, DisplayName: params.DisplayName, CreatedAt: createdAt, UpdatedAt: updatedAt},
		Credentials: Credentials{UserID: params.ID, PasswordHash: params.PasswordHash, PasswordAlgo: params.PasswordAlgo, CreatedAt: time.Now(), UpdatedAt: time.Now(), LastRotated: time.Now()},
		Settings:    Settings{UserID: params.ID, Preferences: params.Preferences, Timezone: params.Timezone, Locale: params.Locale, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}, nil
}

func (r *PostgresRepository) GetByEmail(ctx context.Context, email string) (Aggregate, error) {
	return r.fetch(ctx, `LOWER(email)=LOWER($1)`, email)
}

func (r *PostgresRepository) GetByID(ctx context.Context, id string) (Aggregate, error) {
	return r.fetch(ctx, `id=$1`, id)
}

func (r *PostgresRepository) UpdateLastLogin(ctx context.Context, id string) error {
	query := `UPDATE users SET last_login_at=NOW(), updated_at=NOW() WHERE id=$1`
	res, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("update last login: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *PostgresRepository) fetch(ctx context.Context, where string, arg any) (Aggregate, error) {
	if r == nil || r.db == nil {
		return Aggregate{}, errors.New("users repository not initialized")
	}
	query := fmt.Sprintf(`SELECT u.id, u.email, u.display_name, u.created_at, u.updated_at, u.last_login_at,
		c.password_hash, c.password_algo, c.created_at, c.updated_at, c.last_rotated_at,
		s.preferences, s.timezone, s.locale, s.created_at, s.updated_at
		FROM users u
		JOIN user_credentials c ON c.user_id = u.id
		JOIN user_settings s ON s.user_id = u.id
		WHERE %s`, where)
	var agg Aggregate
	var prefsData []byte
	var lastLogin sql.NullTime
	row := r.db.QueryRowContext(ctx, query, arg)
	if err := row.Scan(&agg.User.ID, &agg.User.Email, &agg.User.DisplayName, &agg.User.CreatedAt, &agg.User.UpdatedAt, &lastLogin,
		&agg.Credentials.PasswordHash, &agg.Credentials.PasswordAlgo, &agg.Credentials.CreatedAt, &agg.Credentials.UpdatedAt, &agg.Credentials.LastRotated,
		&prefsData, &agg.Settings.Timezone, &agg.Settings.Locale, &agg.Settings.CreatedAt, &agg.Settings.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Aggregate{}, ErrUserNotFound
		}
		return Aggregate{}, err
	}
	agg.User.LastLoginAt = nil
	if lastLogin.Valid {
		t := lastLogin.Time
		agg.User.LastLoginAt = &t
	}
	agg.Credentials.UserID = agg.User.ID
	agg.Settings.UserID = agg.User.ID
	_ = json.Unmarshal(prefsData, &agg.Settings.Preferences)
	if agg.Settings.Preferences == nil {
		agg.Settings.Preferences = map[string]any{}
	}
	return agg, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
