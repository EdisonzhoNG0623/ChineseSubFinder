package ifaces

import (
	"context"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/sirupsen/logrus"
)

// Optional context-aware supplier capabilities extend ISupplier without
// breaking existing providers. Search orchestration prefers these interfaces
// when implemented and falls back to the legacy methods otherwise.
type IMovieSupplierContext interface {
	GetSubListFromFile4MovieContext(context.Context, string) ([]supplier.SubInfo, error)
}

type ISeriesSupplierContext interface {
	GetSubListFromFile4SeriesContext(context.Context, *series.SeriesInfo) ([]supplier.SubInfo, error)
}

type ISupplier interface {
	CheckAlive() (bool, int64)

	IsAlive() bool

	GetSupplierName() string

	OverDailyDownloadLimit() bool

	GetLogger() *logrus.Logger

	GetSubListFromFile4Movie(filePath string) ([]supplier.SubInfo, error)

	GetSubListFromFile4Series(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error)

	GetSubListFromFile4Anime(seriesInfo *series.SeriesInfo) ([]supplier.SubInfo, error)
}
