package movie_helper

import (
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

type concurrentMovieSupplier struct {
	name    string
	started chan<- string
	release <-chan struct{}
	panic   bool
}

func (s *concurrentMovieSupplier) CheckAlive() (bool, int64)    { return true, 0 }
func (s *concurrentMovieSupplier) IsAlive() bool                { return true }
func (s *concurrentMovieSupplier) GetSupplierName() string      { return s.name }
func (s *concurrentMovieSupplier) OverDailyDownloadLimit() bool { return false }
func (s *concurrentMovieSupplier) GetLogger() *logrus.Logger    { return log_helper.GetLogger4Tester() }
func (s *concurrentMovieSupplier) GetSubListFromFile4Movie(string) ([]supplier.SubInfo, error) {
	s.started <- s.name
	<-s.release
	if s.panic {
		panic("supplier test panic")
	}
	return []supplier.SubInfo{{Name: s.name}}, nil
}
func (s *concurrentMovieSupplier) GetSubListFromFile4Series(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return nil, nil
}
func (s *concurrentMovieSupplier) GetSubListFromFile4Anime(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return nil, nil
}

func TestOneMovieDlSubInAllSiteRunsSuppliersConcurrentlyAndIsolatesPanic(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	suppliers := []ifaces.ISupplier{
		&concurrentMovieSupplier{name: "ok", started: started, release: release},
		&concurrentMovieSupplier{name: "panic", started: started, release: release, panic: true},
	}

	done := make(chan []supplier.SubInfo, 1)
	go func() {
		done <- OneMovieDlSubInAllSite(log_helper.GetLogger4Tester(), suppliers, "/movie.mkv", 1)
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
