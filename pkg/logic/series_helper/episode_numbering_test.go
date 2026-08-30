package series_helper

import (
	"context"
	"errors"
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

type ambiguousIdentityResolver struct {
	candidates []episode_identity.Identity
}

type matchingAnimeResolver struct {
	matched bool
}

func (r matchingAnimeResolver) Resolve(context.Context, episode_identity.Request) (episode_identity.Identity, error) {
	return episode_identity.Identity{}, episode_identity.ErrNoMapping
}

func (r matchingAnimeResolver) MatchesSeries(context.Context, episode_identity.Request) (bool, error) {
	return r.matched, nil
}

func (r ambiguousIdentityResolver) Resolve(context.Context, episode_identity.Request) (episode_identity.Identity, error) {
	return episode_identity.Identity{}, errors.New("ambiguous mapping")
}

func (r ambiguousIdentityResolver) ResolveCandidates(context.Context, episode_identity.Request) ([]episode_identity.Identity, error) {
	return r.candidates, nil
}

func TestEnrichSeriesEpisodeNumberingUsesAIOnlyWithinCandidates(t *testing.T) {
	seriesInfo := &series.SeriesInfo{
		Name: "Ambiguous Show", Aliases: []string{"Ambiguous Show 2024"},
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{
			"S2E1": {Season: 2, Episode: 1, FileFullPath: "/media/private/path.mkv"},
		},
	}
	resolver := ambiguousIdentityResolver{candidates: []episode_identity.Identity{
		{Season: 2, Episode: 1, AbsoluteEpisode: 13, Confidence: 0.92},
		{Season: 2, Episode: 1, AbsoluteEpisode: 25, Confidence: 0.90},
	}}
	ai := episode_identity.AmbiguityResolverFunc(func(_ context.Context, request episode_identity.AmbiguityRequest) (episode_identity.AmbiguityResult, error) {
		if request.Media.FileName != "" {
			t.Fatalf("AI request leaked file name: %q", request.Media.FileName)
		}
		if len(request.Candidates) != 2 || request.Candidates[1].AbsoluteEpisode != 25 {
			t.Fatalf("unexpected candidates: %#v", request.Candidates)
		}
		return episode_identity.AmbiguityResult{
			SchemaVersion: episode_identity.AmbiguitySchemaVersion, Decision: episode_identity.AmbiguityMatch,
			CandidateID: "identity-1", Confidence: 0.89, Model: "test-model", ModelVersion: "test-v1",
		}, nil
	})

	count, err := enrichSeriesEpisodeNumberingWithResolvers(context.Background(), resolver, ai, seriesInfo)
	if err != nil {
		t.Fatal(err)
	}
	got := seriesInfo.NeedDlEpsKeyList["S2E1"]
	if count != 1 || got.AbsoluteEpisode != 25 || got.NumberingSource != "AI ambiguity" {
		t.Fatalf("AI-selected candidate was not applied: %#v", got)
	}
}

func TestResolveEpisodeIdentityMarksAIAlongsideDeterministicEvidence(t *testing.T) {
	resolver := ambiguousIdentityResolver{candidates: []episode_identity.Identity{
		{Season: 1, Episode: 1, AbsoluteEpisode: 1, Confidence: 0.92, Evidence: []episode_identity.Evidence{{Source: "Anime-Lists title", Confidence: 0.92}}},
		{Season: 1, Episode: 1, AbsoluteEpisode: 13, Confidence: 0.90, Evidence: []episode_identity.Evidence{{Source: "Anime-Lists title", Confidence: 0.90}}},
	}}
	ai := episode_identity.AmbiguityResolverFunc(func(context.Context, episode_identity.AmbiguityRequest) (episode_identity.AmbiguityResult, error) {
		return episode_identity.AmbiguityResult{SchemaVersion: episode_identity.AmbiguitySchemaVersion,
			Decision: episode_identity.AmbiguityMatch, CandidateID: "identity-1", Confidence: 0.9,
			Model: "test-model", ModelVersion: "test-v1"}, nil
	})
	seriesInfo := &series.SeriesInfo{Name: "Show", NeedDlEpsKeyList: map[string]series.EpisodeInfo{
		"S1E1": {Season: 1, Episode: 1},
	}}
	_, err := enrichSeriesEpisodeNumberingWithResolvers(context.Background(), resolver, ai, seriesInfo)
	if err != nil {
		t.Fatal(err)
	}
	if got := seriesInfo.NeedDlEpsKeyList["S1E1"].NumberingSource; got != "Anime-Lists title + AI ambiguity" {
		t.Fatalf("numbering source = %q", got)
	}
}

func TestEnrichSeriesEpisodeNumberingAbstainsWithoutChangingEpisode(t *testing.T) {
	seriesInfo := &series.SeriesInfo{Name: "Ambiguous Show", NeedDlEpsKeyList: map[string]series.EpisodeInfo{
		"S1E1": {Season: 1, Episode: 1},
	}}
	resolver := ambiguousIdentityResolver{candidates: []episode_identity.Identity{
		{Season: 1, Episode: 1, AbsoluteEpisode: 1}, {Season: 1, Episode: 1, AbsoluteEpisode: 13},
	}}
	count, err := enrichSeriesEpisodeNumberingWithResolvers(context.Background(), resolver, episode_identity.DisabledAmbiguityResolver{}, seriesInfo)
	if err != nil || count != 0 || seriesInfo.NeedDlEpsKeyList["S1E1"].AbsoluteEpisode != 0 {
		t.Fatalf("abstention must preserve deterministic state: count=%d err=%v info=%#v", count, err, seriesInfo)
	}
}

func TestContiguousInventoryAbsoluteFallbackMapsSplitAnimeSeasons(t *testing.T) {
	inventory := make([]series.EpisodeInfo, 0, 154)
	for season, count := range []int{33, 32, 8, 68, 13} {
		for episode := 1; episode <= count; episode++ {
			inventory = append(inventory, series.EpisodeInfo{Season: season + 1, Episode: episode})
		}
	}
	info := &series.SeriesInfo{
		IsAnime:       true,
		ArchiveEpList: inventory,
		EpList:        []series.EpisodeInfo{{Season: 4, Episode: 35}, {Season: 4, Episode: 36}},
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{
			"S4E35": {Season: 4, Episode: 35},
			"S4E36": {Season: 4, Episode: 36},
		},
	}
	if got := applyContiguousInventoryAbsoluteFallback(info); got != 2 {
		t.Fatalf("resolved count = %d, want 2", got)
	}
	if got := info.NeedDlEpsKeyList["S4E35"]; got.AbsoluteEpisode != 108 || got.NumberingSource != "local contiguous season inventory" {
		t.Fatalf("S4E35 fallback = %#v, want absolute 108", got)
	}
	if got := info.NeedDlEpsKeyList["S4E36"].AbsoluteEpisode; got != 109 {
		t.Fatalf("S4E36 absolute = %d, want 109", got)
	}
}

func TestContiguousInventoryAbsoluteFallbackRejectsGap(t *testing.T) {
	info := &series.SeriesInfo{
		IsAnime: true,
		ArchiveEpList: []series.EpisodeInfo{
			{Season: 1, Episode: 1}, {Season: 1, Episode: 3}, {Season: 2, Episode: 1},
		},
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{"S2E1": {Season: 2, Episode: 1}},
	}
	if got := applyContiguousInventoryAbsoluteFallback(info); got != 0 || info.NeedDlEpsKeyList["S2E1"].AbsoluteEpisode != 0 {
		t.Fatalf("gapped inventory must abstain: count=%d info=%#v", got, info)
	}
}

func TestContiguousInventoryAbsoluteFallbackRejectsOrdinarySeries(t *testing.T) {
	info := &series.SeriesInfo{
		ArchiveEpList:    []series.EpisodeInfo{{Season: 1, Episode: 1}, {Season: 2, Episode: 1}},
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{"S2E1": {Season: 2, Episode: 1}},
	}
	if got := applyContiguousInventoryAbsoluteFallback(info); got != 0 {
		t.Fatalf("ordinary series fallback count = %d, want 0", got)
	}
}

func TestContiguousInventoryAbsoluteFallbackAbstainsForNewestSeason(t *testing.T) {
	info := &series.SeriesInfo{
		IsAnime: true,
		ArchiveEpList: []series.EpisodeInfo{
			{Season: 1, Episode: 1}, {Season: 1, Episode: 2}, {Season: 2, Episode: 1},
		},
		NeedDlEpsKeyList: map[string]series.EpisodeInfo{"S2E1": {Season: 2, Episode: 1}},
	}
	if got := applyContiguousInventoryAbsoluteFallback(info); got != 0 {
		t.Fatalf("newest season fallback count = %d, want 0", got)
	}
}

func TestDetectAnimeSeriesUsesResolverMetadata(t *testing.T) {
	info := &series.SeriesInfo{TvdbId: "80975", Name: "家庭教师HITMAN REBORN!"}
	matched, err := detectAnimeSeries(context.Background(), matchingAnimeResolver{matched: true}, info)
	if err != nil || !matched {
		t.Fatalf("anime metadata match = %t, %v", matched, err)
	}
	matched, err = detectAnimeSeries(context.Background(), matchingAnimeResolver{matched: false}, info)
	if err != nil || matched {
		t.Fatalf("ordinary metadata match = %t, %v", matched, err)
	}
}

func TestHasAnimeLookupIdentityRequiresSearchableMetadata(t *testing.T) {
	if hasAnimeLookupIdentity(nil) || hasAnimeLookupIdentity(&series.SeriesInfo{Aliases: []string{"  "}}) {
		t.Fatal("empty metadata was considered searchable")
	}
	for _, info := range []*series.SeriesInfo{
		{Name: "Example"}, {ImdbId: "tt123"}, {TmdbId: "42"}, {TvdbId: "7"}, {Aliases: []string{"Alias"}},
	} {
		if !hasAnimeLookupIdentity(info) {
			t.Fatalf("searchable metadata was ignored: %+v", info)
		}
	}
}
