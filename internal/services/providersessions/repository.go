package providersessions

import "context"

// Repository defines persistence behaviour for provider sessions.
type Repository interface {
	Upsert(ctx context.Context, params UpsertParams) (Session, error)
	Get(ctx context.Context, userID, provider string) (Session, error)
	UpdateUsage(ctx context.Context, params UsageUpdateParams) (Session, error)
	Delete(ctx context.Context, userID, provider string) error
}
