package providersessions

import "context"

// Repository defines persistence behaviour for conversational sessions.
type Repository interface {
	Create(ctx context.Context, params CreateParams) (Session, error)
	Get(ctx context.Context, userID, sessionID string) (Session, error)
	List(ctx context.Context, params ListParams) ([]Session, error)
	UpdateUsage(ctx context.Context, params UsageUpdateParams) (Session, error)
	Archive(ctx context.Context, userID, sessionID string) error
}
