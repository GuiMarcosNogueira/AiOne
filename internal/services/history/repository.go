package history

import "context"

// Repository persists context history per user/provider.
type Repository interface {
	Insert(ctx context.Context, params InsertParams) (Entry, error)
	List(ctx context.Context, userID, provider string) ([]Entry, error)
	DeleteAll(ctx context.Context, userID, provider string) error
	DeleteIDs(ctx context.Context, ids []int64) error
}

type InsertParams struct {
	UserID          string
	ProviderName    string
	Role            string
	Message         string
	MediaType       string
	MediaPath       string
	TokensEstimated int
}
