package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

// PostgresRepository stores history entries in PostgreSQL.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository constructs a repository backed by Postgres.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) ensure() error {
	if r == nil || r.db == nil {
		return errors.New("history repository not initialized")
	}
	return nil
}

// Insert saves a history entry.
func (r *PostgresRepository) Insert(ctx context.Context, params InsertParams) (Entry, error) {
	if err := r.ensure(); err != nil {
		return Entry{}, err
	}
	query := `INSERT INTO user_context_history
		(user_id, provider_name, role, message, media_type, media_path, tokens_estimated)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, user_id, provider_name, role, message, media_type, media_path, tokens_estimated, created_at`
	row := r.db.QueryRowContext(ctx, query,
		params.UserID,
		params.ProviderName,
		params.Role,
		params.Message,
		params.MediaType,
		params.MediaPath,
		params.TokensEstimated,
	)
	return scanEntry(row)
}

// List returns all entries ordered by creation time.
func (r *PostgresRepository) List(ctx context.Context, userID, provider string) ([]Entry, error) {
	if err := r.ensure(); err != nil {
		return nil, err
	}
	query := `SELECT id, user_id, provider_name, role, message, media_type, media_path, tokens_estimated, created_at
		FROM user_context_history
		WHERE user_id=$1 AND provider_name=$2
		ORDER BY created_at ASC`
	rows, err := r.db.QueryContext(ctx, query, userID, provider)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// DeleteAll removes every entry for the user/provider pair.
func (r *PostgresRepository) DeleteAll(ctx context.Context, userID, provider string) error {
	if err := r.ensure(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM user_context_history WHERE user_id=$1 AND provider_name=$2`, userID, provider)
	return err
}

// DeleteIDs removes the provided entry ids.
func (r *PostgresRepository) DeleteIDs(ctx context.Context, ids []int64) error {
	if err := r.ensure(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	query := `DELETE FROM user_context_history WHERE id = ANY($1)`
	_, err := r.db.ExecContext(ctx, query, pq.Int64Array(ids))
	return err
}

func scanEntry(row rowScanner) (Entry, error) {
	var e Entry
	if err := row.Scan(&e.ID, &e.UserID, &e.ProviderName, &e.Role, &e.Message, &e.MediaType, &e.MediaPath, &e.TokensEstimated, &e.CreatedAt); err != nil {
		return Entry{}, fmt.Errorf("scan history entry: %w", err)
	}
	return e, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}
