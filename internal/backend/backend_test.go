package backend

import (
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/pre_job"
	"github.com/sirupsen/logrus"
)

func TestShouldSkipPreJob(t *testing.T) {
	tests := []struct {
		name         string
		speedDevMode bool
		liteMode     bool
		want         bool
	}{
		{name: "full mode", liteMode: false, want: true},
		{name: "speed dev lite mode", speedDevMode: true, liteMode: true, want: true},
		{name: "normal lite mode", liteMode: true, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldSkipPreJob(test.speedDevMode, test.liteMode); got != test.want {
				t.Fatalf("shouldSkipPreJob(%t, %t) = %t, want %t", test.speedDevMode, test.liteMode, got, test.want)
			}
		})
	}
}

func TestFinishPreJobMarksDone(t *testing.T) {
	logger := logrus.New()
	job := pre_job.NewPreJob(logger)
	backend := &BackEnd{logger: logger, preJob: job}

	backend.finishPreJob()

	if !job.IsDone() {
		t.Fatal("finishPreJob() left pre-job incomplete")
	}
}
