package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/midia/aione/internal/providers"
	healthsvc "github.com/midia/aione/internal/services/health"
)

func TestHealthServiceAggregatesStatuses(t *testing.T) {
	providersList := []providers.Provider{
		&stubProvider{name: "stable"},
		&stubProvider{name: "flaky", healthErr: errors.New("timeout")},
	}
	svc := healthsvc.NewService(providersList)
	statuses := svc.Check(context.Background())
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
	if !statuses[0].Healthy || statuses[0].Name != "stable" {
		t.Fatalf("expected stable provider to be healthy, got %+v", statuses[0])
	}
	if statuses[1].Healthy || statuses[1].Error == "" {
		t.Fatalf("expected flaky provider to report error, got %+v", statuses[1])
	}
	if time.Since(statuses[0].Checked) > time.Second {
		t.Fatalf("expected checked timestamp to be recent")
	}
}
