package downloader

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

type shutdownFlushStub struct {
	calls int
	err   error
}

func (s *shutdownFlushStub) FlushDirtyPriorities() error {
	s.calls++
	return s.err
}

func TestFlushTaskQueueOnShutdown(t *testing.T) {
	logger := logrus.New()
	var output bytes.Buffer
	logger.SetOutput(&output)
	logger.SetFormatter(&logrus.JSONFormatter{})
	queue := &shutdownFlushStub{err: errors.New("injected flush failure")}

	flushTaskQueueOnShutdown(logger, queue)

	if queue.calls != 1 {
		t.Fatalf("flush calls = %d, want 1", queue.calls)
	}
	logged := output.String()
	if !strings.Contains(logged, `"event":"task_queue_shutdown_flush"`) ||
		!strings.Contains(logged, `"phase":"after_worker_join"`) ||
		!strings.Contains(logged, "injected flush failure") {
		t.Fatalf("shutdown flush warning was not structured: %s", logged)
	}
}

func TestFlushTaskQueueOnShutdownAcceptsNilQueue(t *testing.T) {
	flushTaskQueueOnShutdown(logrus.New(), nil)
}
