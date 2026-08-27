package assrt

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitRateLimitFirstRequestIsImmediate(t *testing.T) {
	supplier := &Supplier{theSearchInterval: time.Second}
	started := time.Now()
	if err := supplier.waitRateLimit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("first request was delayed by %s", elapsed)
	}
}

func TestWaitRateLimitHonorsCancellation(t *testing.T) {
	supplier := &Supplier{theSearchInterval: time.Second, lastRequestAt: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := supplier.waitRateLimit(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitRateLimit() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("cancelled wait returned too slowly: %s", elapsed)
	}
}
