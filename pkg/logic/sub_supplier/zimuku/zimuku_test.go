package zimuku

import (
	"context"
	"errors"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/ifaces"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
)

var (
	_ ifaces.IMovieSupplierContext  = (*Supplier)(nil)
	_ ifaces.ISeriesSupplierContext = (*Supplier)(nil)
	_ ifaces.IAnimeSupplierContext  = (*Supplier)(nil)
)

func TestLimitSubInfosHandlesShortResults(t *testing.T) {
	tests := []struct {
		name    string
		count   int
		maximum int
		want    int
	}{
		{name: "empty", count: 0, maximum: 5, want: 0},
		{name: "short", count: 3, maximum: 5, want: 3},
		{name: "exact", count: 5, maximum: 5, want: 5},
		{name: "truncate", count: 7, maximum: 5, want: 5},
		{name: "disabled", count: 3, maximum: 0, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := make(SubInfos, test.count)
			if got := len(limitSubInfos(input, test.maximum)); got != test.want {
				t.Fatalf("limitSubInfos(count=%d, maximum=%d) = %d, want %d", test.count, test.maximum, got, test.want)
			}
		})
	}
}

func TestContextMethodsStopBeforeStartingBrowser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	supplier := &Supplier{}
	if _, err := supplier.GetSubListFromFile4MovieContext(ctx, "movie.mkv"); !errors.Is(err, context.Canceled) {
		t.Fatalf("movie context error = %v, want context.Canceled", err)
	}
	if _, err := supplier.GetSubListFromFile4SeriesContext(ctx, &series.SeriesInfo{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("series context error = %v, want context.Canceled", err)
	}
}
