package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

func TestConnectSuccess(t *testing.T) {
	t.Cleanup(resetSQLOverrides)
	fakeDB := sql.OpenDB(stubConnector{})
	openSQL = func(name, dsn string) (*sql.DB, error) {
		if name != driverName {
			t.Fatalf("unexpected driver name: %s", name)
		}
		return fakeDB, nil
	}
	db, err := Connect(context.Background(), "test-dsn")
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	if db != fakeDB {
		t.Fatalf("expected db instance to be returned")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestConnectOpenError(t *testing.T) {
	t.Cleanup(resetSQLOverrides)
	expected := errors.New("open failed")
	openSQL = func(name, dsn string) (*sql.DB, error) {
		return nil, expected
	}
	if _, err := Connect(context.Background(), "broken"); !errors.Is(err, expected) {
		t.Fatalf("expected open error, got %v", err)
	}
}

func TestConnectPingError(t *testing.T) {
	t.Cleanup(resetSQLOverrides)
	fakeDB := sql.OpenDB(stubConnector{pingErr: errors.New("ping boom")})
	openSQL = func(name, dsn string) (*sql.DB, error) {
		return fakeDB, nil
	}
	if _, err := Connect(context.Background(), "dsn"); err == nil || !strings.Contains(err.Error(), "ping") {
		t.Fatalf("expected ping error, got %v", err)
	}
}

func resetSQLOverrides() {
	openSQL = sql.Open
	driverName = "pgx"
}

type stubConnector struct {
	pingErr error
}

func (s stubConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return &stubConn{pingErr: s.pingErr}, nil
}

func (s stubConnector) Driver() driver.Driver { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(name string) (driver.Conn, error) { return &stubConn{}, nil }

type stubConn struct {
	pingErr error
}

func (s *stubConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (s *stubConn) Close() error              { return nil }
func (s *stubConn) Begin() (driver.Tx, error) { return nil, errors.New("not implemented") }

func (s *stubConn) Ping(ctx context.Context) error {
	return s.pingErr
}
