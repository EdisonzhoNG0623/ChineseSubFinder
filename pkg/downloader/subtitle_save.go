package downloader

import (
	"context"
	"errors"
	"fmt"
)

// runSubtitleSaveWithContext checks cancellation around a synchronous save.
// The underlying subtitle writer is not context-aware, so returning as soon as
// ctx is canceled would leave it writing after the worker and shared cleanup
// have exited. Waiting here guarantees that no background save remains active.
// A successful save that finishes after the per-job deadline is still a real,
// durable success and must not be retried. Administrative cancellation remains
// observable so shutdown can release the queue claim without recording an
// outcome.
func runSubtitleSaveWithContext(ctx context.Context, save func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	saveErr := func() (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("subtitle save panic: %v", recovered)
			}
		}()
		return save()
	}()
	if saveErr != nil {
		return saveErr
	}
	if err := ctx.Err(); errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
