package providersessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// PostgresRepository persists chat sessions in PostgreSQL.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a PostgreSQL-backed repository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Create inserts a new chat session.
func (r *PostgresRepository) Create(ctx context.Context, params CreateParams) (Session, error) {
	if err := r.ensure(); err != nil {
		return Session{}, err
	}
	metadata := normalizeMetadata(params.Metadata)
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return Session{}, fmt.Errorf("marshal metadata: %w", err)
	}
	query := `INSERT INTO chat_sessions
		(id, user_id, provider_name, title, session_metadata, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, user_id, provider_name, title, session_metadata, expires_at,
			last_interaction, total_tokens_used, created_at, updated_at, archived_at`
	row := r.db.QueryRowContext(ctx, query,
		params.ID,
		params.UserID,
		params.ProviderName,
		params.Title,
		metaJSON,
		params.ExpiresAt,
	)
	return scanSession(row)
}

// Get loads a session scoped to the user.
func (r *PostgresRepository) Get(ctx context.Context, userID, sessionID string) (Session, error) {
	if err := r.ensure(); err != nil {
		return Session{}, err
	}
	query := `SELECT id, user_id, provider_name, title, session_metadata, expires_at,
		last_interaction, total_tokens_used, created_at, updated_at, archived_at
		FROM chat_sessions WHERE id=$1 AND user_id=$2`
	row := r.db.QueryRowContext(ctx, query, sessionID, userID)
	return scanSession(row)
}

// List returns recent sessions for a user/provider.
func (r *PostgresRepository) List(ctx context.Context, params ListParams) ([]Session, error) {
	if err := r.ensure(); err != nil {
		return nil, err
	}
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	base := `SELECT id, user_id, provider_name, title, session_metadata, expires_at,
		last_interaction, total_tokens_used, created_at, updated_at, archived_at
		FROM chat_sessions WHERE user_id=$1`
	args := []any{params.UserID}
	idx := 2
	if params.ProviderName != "" {
		base += fmt.Sprintf(" AND provider_name=$%d", idx)
		args = append(args, params.ProviderName)
		idx++
	}
	if !params.IncludeArchived {
		base += " AND archived_at IS NULL"
	}
	base += fmt.Sprintf(" ORDER BY last_interaction DESC LIMIT $%d", idx)
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// UpdateUsage increments counters and updates metadata.
func (r *PostgresRepository) UpdateUsage(ctx context.Context, params UsageUpdateParams) (Session, error) {
	if err := r.ensure(); err != nil {
		return Session{}, err
	}
	metadata := normalizeMetadata(params.Metadata)
	var metaJSON []byte
	var err error
	if metadata != nil {
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			return Session{}, fmt.Errorf("marshal metadata: %w", err)
		}
	}
	query := `UPDATE chat_sessions
		SET total_tokens_used = total_tokens_used + $3,
			last_interaction = $4,
			session_metadata = COALESCE($5::jsonb, session_metadata),
			expires_at = COALESCE($6, expires_at),
			updated_at = NOW()
		WHERE id=$1 AND user_id=$2
		RETURNING id, user_id, provider_name, title, session_metadata, expires_at,
			last_interaction, total_tokens_used, created_at, updated_at, archived_at`
	row := r.db.QueryRowContext(ctx, query,
		params.SessionID,
		params.UserID,
		params.TokensDelta,
		params.LastInteraction,
		nullableJSON(metaJSON),
		params.ExpiresAt,
	)
	return scanSession(row)
}

// Archive soft deletes a session.
func (r *PostgresRepository) Archive(ctx context.Context, userID, sessionID string) error {
	if err := r.ensure(); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE chat_sessions SET archived_at = NOW(), updated_at = NOW()
		WHERE id=$1 AND user_id=$2 AND archived_at IS NULL`, sessionID, userID)
	if err != nil {
		return fmt.Errorf("archive session: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (r *PostgresRepository) ensure() error {
	if r == nil || r.db == nil {
		return errors.New("providersessions repository not initialized")
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(row rowScanner) (Session, error) {
	var sess Session
	var metaData []byte
	var expires sql.NullTime
	var archived sql.NullTime
	if err := row.Scan(
		&sess.ID,
		&sess.UserID,
		&sess.ProviderName,
		&sess.Title,
		&metaData,
		&expires,
		&sess.LastInteraction,
		&sess.TotalTokensUsed,
		&sess.CreatedAt,
		&sess.UpdatedAt,
		&archived,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrSessionNotFound
		}
		return Session{}, fmt.Errorf("scan session: %w", err)
	}
	if expires.Valid {
		et := expires.Time
		sess.ExpiresAt = &et
	}
	if archived.Valid {
		at := archived.Time
		sess.ArchivedAt = &at
	}
	if len(metaData) > 0 {
		_ = json.Unmarshal(metaData, &sess.Metadata)
	}
	if sess.Metadata == nil {
		sess.Metadata = map[string]any{}
	}
	return sess, nil
}

func normalizeMetadata(meta map[string]any) map[string]any {
	if meta == nil {
		return map[string]any{}
	}
	return meta
}

func nullableJSON(b []byte) any {
	if b == nil {
		return nil
	}
	return b
}
