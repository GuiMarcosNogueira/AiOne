package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	openSQL    = sql.Open
	driverName = "pgx"
)

// Connect opens a PostgreSQL connection using the pgx stdlib driver.
func Connect(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := openSQL(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}
