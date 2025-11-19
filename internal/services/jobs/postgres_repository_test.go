package jobs

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresRepositoryCreate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := NewPostgresRepository(db)
	job := Job{ID: "job", Type: TypeVideo, Status: StatusPending, Prompt: "prompt", CallbackAttempts: 1, MaxCallbackAttempts: 3}
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO jobs")).
		WithArgs(job.ID, job.Type, job.Status, job.Prompt, sqlmock.AnyArg(), sqlmock.AnyArg(), job.ErrorMessage,
			job.FilePath, job.CallbackURL, job.CallbackAttempts, job.MaxCallbackAttempts, job.NextCallbackAttempt).
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))

	created, err := repo.Create(context.Background(), job)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.ID != job.ID {
		t.Fatalf("expected job id to round-trip")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresRepositoryGetBehavior(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewPostgresRepository(db)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, type, status")).
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)
	if _, err := repo.Get(context.Background(), "missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}

	payload := []byte(`{"foo":"bar"}`)
	result := []byte(`{"url":"video"}`)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, type, status")).
		WithArgs("job").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "type", "status", "prompt", "payload", "result", "error_message", "file_path", "callback_url",
			"callback_attempts", "max_callback_attempts", "next_callback_attempt", "created_at", "updated_at", "last_dispatched_at", "last_callback_response",
		}).AddRow("job", TypeVideo, StatusCompleted, "prompt", payload, result, "", "file", "cb",
			1, 3, now, now, now, now, "status=200"))
	got, err := repo.Get(context.Background(), "job")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Payload["foo"] != "bar" {
		t.Fatalf("expected payload to unmarshal, got %+v", got.Payload)
	}
	if got.Result["url"] != "video" {
		t.Fatalf("expected result payload, got %+v", got.Result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresRepositoryUpdate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewPostgresRepository(db)
	job := Job{ID: "job", Status: StatusCompleted, Prompt: "p", CallbackAttempts: 2, MaxCallbackAttempts: 3}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE jobs SET")).
		WithArgs(job.ID, job.Status, job.Prompt, sqlmock.AnyArg(), sqlmock.AnyArg(), job.ErrorMessage, job.FilePath,
			job.CallbackURL, job.CallbackAttempts, job.MaxCallbackAttempts, job.NextCallbackAttempt, job.LastDispatchedAt, job.LastCallbackResponse).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if _, err := repo.Update(context.Background(), job); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE jobs SET")).
		WithArgs(job.ID, job.Status, job.Prompt, sqlmock.AnyArg(), sqlmock.AnyArg(), job.ErrorMessage, job.FilePath,
			job.CallbackURL, job.CallbackAttempts, job.MaxCallbackAttempts, job.NextCallbackAttempt, job.LastDispatchedAt, job.LastCallbackResponse).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := repo.Update(context.Background(), job); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresRepositoryClaimPending(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewPostgresRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("WITH cte AS")).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()
	job, err := repo.ClaimPending(context.Background())
	if err != nil || job != nil {
		t.Fatalf("expected no job, err=%v job=%v", err, job)
	}

	payload := []byte(`{}`)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("WITH cte AS")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("job"))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, type, status")).
		WithArgs("job").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "type", "status", "prompt", "payload", "result", "error_message", "file_path", "callback_url",
			"callback_attempts", "max_callback_attempts", "next_callback_attempt", "created_at", "updated_at", "last_dispatched_at", "last_callback_response",
		}).AddRow("job", TypeVideo, StatusRunning, "prompt", payload, payload, "", "file", "cb",
			0, 1, now, now, now, now, ""))
	job, err = repo.ClaimPending(context.Background())
	if err != nil || job == nil {
		t.Fatalf("expected claimed job, err=%v job=%v", err, job)
	}
	if job.ID != "job" {
		t.Fatalf("expected job id, got %s", job.ID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresRepositoryJobsNeedingCallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewPostgresRepository(db)

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM jobs WHERE callback_url")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("job"))
	payload := []byte(`{}`)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, type, status")).
		WithArgs("job").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "type", "status", "prompt", "payload", "result", "error_message", "file_path", "callback_url",
			"callback_attempts", "max_callback_attempts", "next_callback_attempt", "created_at", "updated_at", "last_dispatched_at", "last_callback_response",
		}).AddRow("job", TypeVideo, StatusCompleted, "prompt", payload, payload, "", "file", "cb",
			0, 1, now, now, now, now, ""))

	jobs, err := repo.JobsNeedingCallback(context.Background(), now)
	if err != nil {
		t.Fatalf("JobsNeedingCallback error: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job" {
		t.Fatalf("expected job to be returned, got %+v", jobs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
