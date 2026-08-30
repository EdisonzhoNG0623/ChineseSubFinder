package downloader

import (
	"context"
	"fmt"
)

// runSubtitleSaveWithContext checks cancellation around a synchronous save.
// The underlying subtitle writer is not context-aware, so returning as soon as
// ctx is canceled would leave it writing after the worker and shared cleanup
// have exited. Waiting here guarantees that a returned cancellation is final:
// no background save remains active.
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
	if err := ctx.Err(); err != nil {
		return err
	}
	return saveErr
}
