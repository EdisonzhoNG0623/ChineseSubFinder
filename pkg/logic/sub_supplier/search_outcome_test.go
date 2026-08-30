package sub_supplier

import (
	"context"
	"errors"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/supplier_search"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

type outcomeSupplier struct {
	name string
	err  error
}

func (s *outcomeSupplier) CheckAlive() (bool, int64)    { return true, 0 }
func (s *outcomeSupplier) IsAlive() bool                { return true }
func (s *outcomeSupplier) GetSupplierName() string      { return s.name }
func (s *outcomeSupplier) OverDailyDownloadLimit() bool { return false }
func (s *outcomeSupplier) GetLogger() *logrus.Logger    { return log_helper.GetLogger4Tester() }
func (s *outcomeSupplier) GetSubListFromFile4Movie(string) ([]supplier.SubInfo, error) {
	return []supplier.SubInfo{}, s.err
}
func (s *outcomeSupplier) GetSubListFromFile4Series(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return []supplier.SubInfo{}, s.err
}
func (s *outcomeSupplier) GetSubListFromFile4Anime(*series.SeriesInfo) ([]supplier.SubInfo, error) {
	return []supplier.SubInfo{}, s.err
}

func TestMovieSearchOnlyTreatsConclusiveEmptyAsNoSubtitle(t *testing.T) {
	settings.SetConfigRootPath(t.TempDir())
	healthyHub := NewSubSupplierHub(
		&outcomeSupplier{name: "hub-empty-one"},
		&outcomeSupplier{name: "hub-empty-two"},
	)
	files, err := healthyHub.DownloadSub4MovieContext(context.Background(), "movie.mkv", 1)
	if err != nil || len(files) != 0 {
		t.Fatalf("healthy empty search = files:%v err:%v, want empty without provider error", files, err)
	}

	degradedHub := NewSubSupplierHub(
		&outcomeSupplier{name: "hub-partial-empty"},
		&outcomeSupplier{name: "hub-partial-failure", err: errors.New("connection reset")},
	)
	files, err = degradedHub.DownloadSub4MovieContext(context.Background(), "movie.mkv", 2)
	var searchErr *supplier_search.SearchError
	if len(files) != 0 || !errors.As(err, &searchErr) || searchErr.Kind != supplier_search.FailureTransient {
		t.Fatalf("degraded empty search = files:%v err:%v, want typed transient", files, err)
	}
}
