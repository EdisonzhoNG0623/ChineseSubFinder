package series_helper

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

type dispatchSeriesSupplier struct {
	seriesCalls int
	animeCalls  int
}

func (s *dispatchSeriesSupplier) CheckAlive() (bool, int64)    { return true, 0 }
func (s *dispatchSeriesSupplier) IsAlive() bool                { return true }
func (s *dispatchSeriesSupplier) GetSupplierName() string      { return "dispatch" }
func (s *dispatchSeriesSupplier) OverDailyDownloadLimit() bool { return false }
func (s *dispatchSeriesSupplier) GetLogger() *logrus.Logger    { return log_helper.GetLogger4Tester() }
func (s *dispatchSeriesSupplier) GetSubListFromFile4Movie(string) ([]supplier.SubInfo, error) {
	return nil, nil
}
func (s *dispatchSeriesSupplier) GetSubListFromFile4Series(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	s.seriesCalls++
	return nil, nil
}
func (s *dispatchSeriesSupplier) GetSubListFromFile4Anime(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	s.animeCalls++
	return nil, nil
}

type concurrentSeriesSupplier struct {
	name    string
	started chan<- string
	release <-chan struct{}
	panic   bool
}

var concurrentSeriesTestSequence atomic.Uint64

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
	testID := concurrentSeriesTestSequence.Add(1)
	okName := fmt.Sprintf("ok-%d", testID)
	panicName := fmt.Sprintf("panic-%d", testID)
	suppliers := []ifaces.ISupplier{
		&concurrentSeriesSupplier{name: okName, started: started, release: release},
		&concurrentSeriesSupplier{name: panicName, started: started, release: release, panic: true},
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
		if len(got) != 1 || got[0].Name != okName {
			t.Fatalf("unexpected subtitles after panic isolation: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent supplier call did not finish")
	}
}

func TestDownloadFromSeriesSupplierDispatchesAnime(t *testing.T) {
	supplier := &dispatchSeriesSupplier{}
	if _, err := downloadFromSeriesSupplier(context.Background(), supplier, &series.SeriesInfo{IsAnime: true}); err != nil {
		t.Fatal(err)
	}
	if supplier.animeCalls != 1 || supplier.seriesCalls != 0 {
		t.Fatalf("anime calls=%d series calls=%d", supplier.animeCalls, supplier.seriesCalls)
	}
}
