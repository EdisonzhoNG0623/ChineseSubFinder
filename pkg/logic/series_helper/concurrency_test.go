package series_helper

import (
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

type concurrentSeriesSupplier struct {
	name    string
	started chan<- string
	release <-chan struct{}
	panic   bool
}

func (s *concurrentSeriesSupplier) CheckAlive() (bool, int64)    { return true, 0 }
func (s *concurrentSeriesSupplier) IsAlive() bool                { return true }
func (s *concurrentSeriesSupplier) GetSupplierName() string      { return s.name }
func (s *concurrentSeriesSupplier) OverDailyDownloadLimit() bool { return false }
func (s *concurrentSeriesSupplier) GetLogger() *logrus.Logger    { return log_helper.GetLogger4Tester() }
func (s *concurrentSeriesSupplier) GetSubListFromFile4Movie(string) ([]supplier.SubInfo, error) {
	return nil, nil
}
func (s *concurrentSeriesSupplier) GetSubListFromFile4Series(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	s.started <- s.name
	<-s.release
	if s.panic {
		panic("supplier test panic")
	}
	return []supplier.SubInfo{{Name: s.name}}, nil
}
func (s *concurrentSeriesSupplier) GetSubListFromFile4Anime(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return nil, nil
}

func TestDownloadSubtitleInAllSiteByOneSeriesRunsSuppliersConcurrentlyAndIsolatesPanic(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	suppliers := []ifaces.ISupplier{
		&concurrentSeriesSupplier{name: "ok", started: started, release: release},
		&concurrentSeriesSupplier{name: "panic", started: started, release: release, panic: true},
	}
	seriesInfo := &series.SeriesInfo{
		DirPath: "/series",
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{
			"S01E01": {},
		},
	}

	done := make(chan []supplier.SubInfo, 1)
	go func() {
		done <- DownloadSubtitleInAllSiteByOneSeries(log_helper.GetLogger4Tester(), suppliers, seriesInfo, 1)
	}()

	for i := 0; i < len(suppliers); i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("suppliers did not start concurrently")
		}
	}
	close(release)

	select {
	case got := <-done:
		if len(got) != 1 || got[0].Name != "ok" {
			t.Fatalf("unexpected subtitles after panic isolation: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent supplier call did not finish")
	}
}
