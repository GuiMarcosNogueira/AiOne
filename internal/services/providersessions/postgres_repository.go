package providersessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// PostgresRepository persists provider sessions in PostgreSQL.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a PostgreSQL-backed repository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) Upsert(ctx context.Context, params UpsertParams) (Session, error) {
	if err := r.ensure(); err != nil {
		return Session{}, err
	}
	metadata := normalizeMetadata(params.Metadata)
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		return Session{}, fmt.Errorf("marshal metadata: %w", err)
	}
	query := `INSERT INTO user_provider_sessions
		(user_id, provider_name, encrypted_provider_key, encryption_key_id, session_metadata, expires_at, last_interaction)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id, provider_name)
		DO UPDATE SET encrypted_provider_key=EXCLUDED.encrypted_provider_key,
			encryption_key_id=EXCLUDED.encryption_key_id,
			session_metadata=EXCLUDED.session_metadata,
			expires_at=EXCLUDED.expires_at,
			last_interaction=EXCLUDED.last_interaction,
			updated_at=NOW()
		RETURNING user_id, provider_name, encrypted_provider_key, encryption_key_id, last_interaction,
			total_tokens_used, session_metadata, expires_at, created_at, updated_at`
	row := r.db.QueryRowContext(ctx, query,
		params.UserID,
		params.ProviderName,
		params.EncryptedKey,
		params.EncryptionKeyID,
		metaJSON,
		params.ExpiresAt,
		params.LastInteraction,
	)
	return scanSession(row)
}

func (r *PostgresRepository) Get(ctx context.Context, userID, provider string) (Session, error) {
	if err := r.ensure(); err != nil {
		return Session{}, err
	}
	query := `SELECT user_id, provider_name, encrypted_provider_key, encryption_key_id, last_interaction,
		total_tokens_used, session_metadata, expires_at, created_at, updated_at
		FROM user_provider_sessions WHERE user_id=$1 AND provider_name=$2`
	row := r.db.QueryRowContext(ctx, query, userID, provider)
	return scanSession(row)
}

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
	query := `UPDATE user_provider_sessions
		SET total_tokens_used = total_tokens_used + $3,
			last_interaction = $4,
			session_metadata = COALESCE($5::jsonb, session_metadata),
			expires_at = COALESCE($6, expires_at),
			updated_at = NOW()
		WHERE user_id=$1 AND provider_name=$2
		RETURNING user_id, provider_name, encrypted_provider_key, encryption_key_id, last_interaction,
			total_tokens_used, session_metadata, expires_at, created_at, updated_at`
	row := r.db.QueryRowContext(ctx, query,
		params.UserID,
		params.ProviderName,
		params.TokensDelta,
		params.LastInteraction,
		nullableJSON(metaJSON),
		params.ExpiresAt,
	)
	return scanSession(row)
}

func (r *PostgresRepository) Delete(ctx context.Context, userID, provider string) error {
	if err := r.ensure(); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM user_provider_sessions WHERE user_id=$1 AND provider_name=$2`, userID, provider)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
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
	if err := row.Scan(
		&sess.UserID,
		&sess.ProviderName,
		&sess.EncryptedKey,
		&sess.EncryptionKeyID,
		&sess.LastInteraction,
		&sess.TotalTokensUsed,
		&metaData,
		&expires,
		&sess.CreatedAt,
		&sess.UpdatedAt,
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
