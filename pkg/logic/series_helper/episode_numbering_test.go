package series_helper

import (
	"context"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
)

func TestEnrichSeriesEpisodeNumberingUpdatesRequestedEpisode(t *testing.T) {
	seriesInfo := &series.SeriesInfo{
		ImdbId: "tt1528406", TmdbId: "46261", Name: "Fairy Tail",
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{
			"S8E11": {Season: 8, Episode: 11, FileFullPath: "/anime/Fairy Tail S08E11.mkv"},
		},
		EpList: []series.EpisodeInfo{{Season: 8, Episode: 11}},
	}
	resolver := episode_identity.ResolverFunc(func(_ context.Context, request episode_identity.Request) (episode_identity.Identity, error) {
		if request.IDs.TMDB != "46261" || request.Season != 8 || request.Episode != 11 {
			t.Fatalf("unexpected request: %#v", request)
		}
		return episode_identity.Identity{
			Season: 8, Episode: 11, AbsoluteEpisode: 288, Confidence: 1,
			Evidence: []episode_identity.Evidence{{Source: "Anime-Lists", Confidence: 1}},
		}, nil
	})

	count, err := EnrichSeriesEpisodeNumbering(context.Background(), resolver, seriesInfo)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || seriesInfo.NeedDlEpsKeyList["S8E11"].AbsoluteEpisode != 288 ||
		seriesInfo.EpList[0].AbsoluteEpisode != 288 {
		t.Fatalf("series numbering not enriched: %#v", seriesInfo)
	}
}
