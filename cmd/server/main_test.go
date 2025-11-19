package main

import (
	"context"
	"os"
	"os/signal"
	"testing"
	"time"
)

func TestMainStartsAndStops(t *testing.T) {
	t.Cleanup(func() { notifyContext = signal.NotifyContext })
	ctxCancel := make(chan context.CancelFunc, 1)
	notifyContext = func(parent context.Context, sig ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		ctxCancel <- cancel
		return ctx, cancel
	}
	t.Setenv("HTTP_PORT", "0")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "1")
	t.Setenv("PROVIDER_CB_COOLDOWN", "1")
	t.Setenv("PROVIDER_FAILOVER_ATTEMPTS", "1")
	t.Setenv("PROVIDER_CB_THRESHOLD", "1")
	t.Setenv("PROVIDER_STRATEGY", "first")

	done := make(chan struct{})
	go func() {
		main()
		close(done)
	}()

	cancel := <-ctxCancel
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("main did not exit in time")
	}
}
