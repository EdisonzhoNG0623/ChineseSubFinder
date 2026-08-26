package cron_helper

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestStopMarksIdleRuntimeForRebuild(t *testing.T) {
	ch := &CronHelper{Logger: logrus.New()}

	ch.Stop()

	if !ch.runtimeStopped {
		t.Fatal("Stop must invalidate an idle runtime so the next Start reloads settings")
	}
	if ch.stopping {
		t.Fatal("Stop left the lifecycle in stopping state")
	}
}

func TestRepeatedStopKeepsRuntimeInvalidated(t *testing.T) {
	ch := &CronHelper{Logger: logrus.New()}

	ch.Stop()
	ch.Stop()

	if !ch.runtimeStopped {
		t.Fatal("repeated Stop unexpectedly made the cancelled runtime reusable")
	}
}
