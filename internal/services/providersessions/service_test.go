package providersessions

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCreateSessionStoresMetadata(t *testing.T) {
	svc, repo := newTestService(t)
	expires := time.Now().Add(2 * time.Hour)
	details, err := svc.CreateSession(context.Background(), CreateSessionInput{
		UserID:       "user-1",
		ProviderName: "OpenAI",
		Title:        "Demo",
		Metadata:     map[string]any{"team": "alpha"},
		ExpiresAt:    &expires,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if details.ProviderName != "openai" {
		t.Fatalf("expected provider normalized, got %s", details.ProviderName)
	}
	if len(repo.sessions) != 1 {
		t.Fatalf("expected session stored")
	}
}

func TestCreateSessionValidatesInput(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.CreateSession(context.Background(), CreateSessionInput{}); !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("expected user id error")
	}
	if _, err := svc.CreateSession(context.Background(), CreateSessionInput{UserID: "u"}); !errors.Is(err, ErrProviderRequired) {
		t.Fatalf("expected provider error")
	}
	if _, err := svc.CreateSession(context.Background(), CreateSessionInput{UserID: "u", ProviderName: "openai"}); !errors.Is(err, ErrTitleRequired) {
		t.Fatalf("expected title error")
	}
}

func TestGetSessionValidatesInput(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.GetSession(context.Background(), "", "session"); !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("expected user id error")
	}
	if _, err := svc.GetSession(context.Background(), "user", ""); !errors.Is(err, ErrSessionIDRequired) {
		t.Fatalf("expected session id error")
	}
}

func TestListSessionsFilters(t *testing.T) {
	svc, repo := newTestService(t)
	repo.sessions["session-1"] = Session{ID: "session-1", UserID: "user-1", ProviderName: "openai", Title: "Chat", LastInteraction: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	results, err := svc.ListSessions(context.Background(), ListSessionsInput{UserID: "user-1", ProviderName: "openai"})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 session, got %d", len(results))
	}
}

func TestRecordUsageValidatesInput(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.RecordUsage(context.Background(), UsageInput{}); !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("expected user id error")
	}
	if _, err := svc.RecordUsage(context.Background(), UsageInput{UserID: "u"}); !errors.Is(err, ErrProviderRequired) {
		t.Fatalf("expected provider error")
	}
	if _, err := svc.RecordUsage(context.Background(), UsageInput{UserID: "u", ProviderName: "openai"}); !errors.Is(err, ErrSessionIDRequired) {
		t.Fatalf("expected session id error")
	}
}

func TestRecordUsageUpdatesSession(t *testing.T) {
	svc, repo := newTestService(t)
	repo.sessions["session-1"] = Session{ID: "session-1", UserID: "user-1", ProviderName: "openai", Title: "Chat", LastInteraction: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	details, err := svc.RecordUsage(context.Background(), UsageInput{
		SessionID:       "session-1",
		UserID:          "user-1",
		ProviderName:    "openai",
		TokensDelta:     42,
		Metadata:        map[string]any{"last": "msg"},
		LastInteraction: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if details.TotalTokensUsed != 42 {
		t.Fatalf("expected usage to increment")
	}
}

func TestArchiveSessionValidatesInput(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.ArchiveSession(context.Background(), "", "session"); !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("expected user id error")
	}
	if err := svc.ArchiveSession(context.Background(), "user", ""); !errors.Is(err, ErrSessionIDRequired) {
		t.Fatalf("expected session id error")
	}
}

func TestArchiveSessionMarksArchived(t *testing.T) {
	svc, repo := newTestService(t)
	now := time.Now()
	repo.sessions["session-1"] = Session{ID: "session-1", UserID: "user-1", ProviderName: "openai", Title: "Chat", LastInteraction: now, CreatedAt: now, UpdatedAt: now}
	if err := svc.ArchiveSession(context.Background(), "user-1", "session-1"); err != nil {
		t.Fatalf("archive session: %v", err)
	}
	if repo.sessions["session-1"].ArchivedAt == nil {
		t.Fatalf("expected archived timestamp set")
	}
}

func newTestService(t *testing.T) (*Service, *memoryRepository) {
	t.Helper()
	repo := newMemoryRepository()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, repo
}

type memoryRepository struct {
	sessions map[string]Session
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{sessions: map[string]Session{}}
}

func (m *memoryRepository) Create(ctx context.Context, params CreateParams) (Session, error) {
	now := time.Now().UTC()
	sess := Session{
		ID:              params.ID,
		UserID:          params.UserID,
		ProviderName:    params.ProviderName,
		Title:           params.Title,
		Metadata:        params.Metadata,
		ExpiresAt:       params.ExpiresAt,
		LastInteraction: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	m.sessions[sess.ID] = sess
	return sess, nil
}

func (m *memoryRepository) Get(ctx context.Context, userID, sessionID string) (Session, error) {
	sess, ok := m.sessions[sessionID]
	if !ok || sess.UserID != userID {
		return Session{}, ErrSessionNotFound
	}
	return sess, nil
}

func (m *memoryRepository) List(ctx context.Context, params ListParams) ([]Session, error) {
	var sessions []Session
	for _, sess := range m.sessions {
		if sess.UserID != params.UserID {
			continue
		}
		if params.ProviderName != "" && sess.ProviderName != params.ProviderName {
			continue
		}
		if !params.IncludeArchived && sess.ArchivedAt != nil {
			continue
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

func (m *memoryRepository) UpdateUsage(ctx context.Context, params UsageUpdateParams) (Session, error) {
	sess, ok := m.sessions[params.SessionID]
	if !ok || sess.UserID != params.UserID {
		return Session{}, ErrSessionNotFound
	}
	sess.TotalTokensUsed += params.TokensDelta
	if !params.LastInteraction.IsZero() {
		sess.LastInteraction = params.LastInteraction
	}
	if params.Metadata != nil {
		sess.Metadata = params.Metadata
	}
	if params.ExpiresAt != nil {
		sess.ExpiresAt = params.ExpiresAt
	}
	sess.UpdatedAt = time.Now().UTC()
	m.sessions[sess.ID] = sess
	return sess, nil
}

func (m *memoryRepository) Archive(ctx context.Context, userID, sessionID string) error {
	sess, ok := m.sessions[sessionID]
	if !ok || sess.UserID != userID {
		return ErrSessionNotFound
	}
	now := time.Now().UTC()
	sess.ArchivedAt = &now
	sess.UpdatedAt = now
	m.sessions[sessionID] = sess
	return nil
}
