package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type postgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository wires a PostgreSQL-backed repository.
func NewPostgresRepository(db *sql.DB) Repository {
	return &postgresRepository{db: db}
}

func (p *postgresRepository) Create(ctx context.Context, job Job) (Job, error) {
	payload, _ := json.Marshal(job.Payload)
	result, _ := json.Marshal(job.Result)
	query := `INSERT INTO jobs
		(id, type, status, prompt, payload, result, error_message, file_path, callback_url,
		 callback_attempts, max_callback_attempts, next_callback_attempt, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NOW(),NOW())
		RETURNING created_at, updated_at`
	err := p.db.QueryRowContext(ctx, query,
		job.ID, job.Type, job.Status, job.Prompt, payload, result, job.ErrorMessage,
		job.FilePath, job.CallbackURL, job.CallbackAttempts, job.MaxCallbackAttempts, job.NextCallbackAttempt,
	).Scan(&job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return Job{}, fmt.Errorf("insert job: %w", err)
	}
	return job, nil
}

func (p *postgresRepository) Get(ctx context.Context, id string) (Job, error) {
	query := `SELECT id, type, status, prompt, payload, result, error_message, file_path, callback_url,
		callback_attempts, max_callback_attempts, next_callback_attempt, created_at, updated_at, last_dispatched_at, last_callback_response
		FROM jobs WHERE id = $1`
	var job Job
	var payloadData, resultData []byte
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&job.ID, &job.Type, &job.Status, &job.Prompt, &payloadData, &resultData, &job.ErrorMessage,
		&job.FilePath, &job.CallbackURL, &job.CallbackAttempts, &job.MaxCallbackAttempts, &job.NextCallbackAttempt,
		&job.CreatedAt, &job.UpdatedAt, &job.LastDispatchedAt, &job.LastCallbackResponse,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("select job: %w", err)
	}
	_ = json.Unmarshal(payloadData, &job.Payload)
	_ = json.Unmarshal(resultData, &job.Result)
	return job, nil
}

func (p *postgresRepository) Update(ctx context.Context, job Job) (Job, error) {
	payload, _ := json.Marshal(job.Payload)
	result, _ := json.Marshal(job.Result)
	query := `UPDATE jobs SET
		status=$2,
		prompt=$3,
		payload=$4,
		result=$5,
		error_message=$6,
		file_path=$7,
		callback_url=$8,
		callback_attempts=$9,
		max_callback_attempts=$10,
		next_callback_attempt=$11,
		last_dispatched_at=$12,
		last_callback_response=$13,
		updated_at=NOW()
		WHERE id=$1`
	res, err := p.db.ExecContext(ctx, query,
		job.ID, job.Status, job.Prompt, payload, result, job.ErrorMessage, job.FilePath,
		job.CallbackURL, job.CallbackAttempts, job.MaxCallbackAttempts, job.NextCallbackAttempt,
		job.LastDispatchedAt, job.LastCallbackResponse,
	)
	if err != nil {
		return Job{}, fmt.Errorf("update job: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return Job{}, ErrJobNotFound
	}
	job.UpdatedAt = time.Now()
	return job, nil
}

func (p *postgresRepository) ClaimPending(ctx context.Context) (*Job, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	query := `WITH cte AS (
		SELECT id FROM jobs WHERE status='pending' ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		UPDATE jobs SET status='running', last_dispatched_at=NOW(), updated_at=NOW()
		WHERE id IN (SELECT id FROM cte) RETURNING id`
	var id string
	err = tx.QueryRowContext(ctx, query).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	job, err := p.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (p *postgresRepository) JobsNeedingCallback(ctx context.Context, now time.Time) ([]Job, error) {
	query := `SELECT id FROM jobs WHERE callback_url <> ''
		AND status IN ('completed','failed')
		AND callback_attempts < max_callback_attempts
		AND (next_callback_attempt IS NULL OR next_callback_attempt <= $1)`
	rows, err := p.db.QueryContext(ctx, query, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobsList []Job
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		job, err := p.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		jobsList = append(jobsList, job)
	}
	return jobsList, nil
}
