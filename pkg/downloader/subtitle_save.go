package downloader

import (
	"context"
	"fmt"
)

// runSubtitleSaveWithContext returns exactly one terminal result. A single
// buffered channel avoids racing a successful result against a separately
// closed panic channel.
func runSubtitleSaveWithContext(ctx context.Context, save func() error) error {
	result := make(chan error, 1)
	go func() {
		var saveErr error
		defer func() {
			if recovered := recover(); recovered != nil {
				saveErr = fmt.Errorf("subtitle save panic: %v", recovered)
			}
			result <- saveErr
		}()
		saveErr = save()
	}()

	select {
	case saveErr := <-result:
		return saveErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
