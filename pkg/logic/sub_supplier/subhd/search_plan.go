package subhd

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/Tnze/go.num/v2/zh"
)

const subHDMaxSearchQueries = 12

var subHDCollectionRange = regexp.MustCompile(`(?i)(?:^|[^0-9])[0-9]{1,4}[[:space:]]*[-~～—至][[:space:]]*[0-9]{1,4}(?:[^0-9]|$)`)

type subHDSearchQuery struct {
	Keyword string
	Kind    string
}

func subHDSeriesAliases(mediaInfo *models.MediaInfo, seriesInfo *series.SeriesInfo) []string {
	aliases := make([]string, 0, 4)
	if mediaInfo != nil {
		aliases = append(aliases, mediaInfo.TitleCn, mediaInfo.TitleEn, mediaInfo.OriginalTitle)
	}
	if seriesInfo != nil {
		aliases = append(aliases, seriesInfo.Name)
	}
	return uniqueSubHDAliases(aliases)
}

// buildSubHDSearchPlan puts season-level searches first because one matching
// detail page can satisfy every queued episode in that season. Episode and
// absolute-number variants are fallbacks for anime whose site entry uses a
// different season layout from the local library.
func buildSubHDSearchPlan(aliases []string, season int, episodes []series.EpisodeInfo, anime bool) []subHDSearchQuery {
	queries := make([]subHDSearchQuery, 0, subHDMaxSearchQueries)
	seen := make(map[string]struct{}, subHDMaxSearchQueries)
	appendQuery := func(kind, keyword string) {
		keyword = strings.Join(strings.Fields(keyword), " ")
		if keyword == "" || len(queries) >= subHDMaxSearchQueries {
			return
		}
		key := strings.ToLower(keyword)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		queries = append(queries, subHDSearchQuery{Keyword: keyword, Kind: kind})
	}

	aliases = uniqueSubHDAliases(aliases)
	if season > 0 {
		for _, alias := range aliases {
			appendQuery("season_cn", fmt.Sprintf("%s 第%s季", alias, zh.Uint64(season).String()))
		}
	}

	if anime {
		episodes = append([]series.EpisodeInfo(nil), episodes...)
		sort.SliceStable(episodes, func(i, j int) bool {
			if episodes[i].Season != episodes[j].Season {
				return episodes[i].Season < episodes[j].Season
			}
			return episodes[i].Episode < episodes[j].Episode
		})
		for _, episode := range episodes {
			plan := episode_identity.BuildSearchPlan(aliases, episode_identity.Identity{
				Season: episode.Season, Episode: episode.Episode, AbsoluteEpisode: episode.AbsoluteEpisode,
				SceneSeason: episode.SceneSeason, SceneEpisode: episode.SceneEpisode,
			})
			// Bare absolute numbers are common on anime subtitle sites. Keep
			// them ahead of E/EP/# forms so they survive the provider budget.
			for _, query := range plan {
				if query.Kind == episode_identity.QueryAired {
					appendQuery(strings.ToLower(string(query.Kind)), query.Query)
				}
			}
			bareSuffix := fmt.Sprintf(" %d", episode.AbsoluteEpisode)
			for _, query := range plan {
				if query.Kind == episode_identity.QueryAbsolute && strings.HasSuffix(query.Query, bareSuffix) {
					appendQuery(strings.ToLower(string(query.Kind)), query.Query)
				}
			}
			for _, query := range plan {
				if query.Kind != episode_identity.QueryAired &&
					!(query.Kind == episode_identity.QueryAbsolute && strings.HasSuffix(query.Query, bareSuffix)) {
					appendQuery(strings.ToLower(string(query.Kind)), query.Query)
				}
			}
		}
	}
	if season > 0 {
		for _, alias := range aliases {
			appendQuery("season_token", fmt.Sprintf("%s S%02d", alias, season))
		}
	}
	for _, alias := range aliases {
		appendQuery("series", alias)
	}
	return queries
}

func uniqueSubHDAliases(aliases []string) []string {
	out := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.Join(strings.Fields(alias), " ")
		if alias == "" {
			continue
		}
		key := strings.ToLower(alias)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, alias)
	}
	return out
}

func episodesForSeason(seriesInfo *series.SeriesInfo, season int) []series.EpisodeInfo {
	if seriesInfo == nil {
		return nil
	}
	out := make([]series.EpisodeInfo, 0)
	for _, episode := range seriesInfo.NeedDlEpsKeyList {
		if episode.Season == season {
			out = append(out, episode)
		}
	}
	return out
}

func subHDTargetSeasons(seriesInfo *series.SeriesInfo) []int {
	if seriesInfo == nil {
		return nil
	}
	seasonSet := make(map[int]struct{})
	for season := range seriesInfo.NeedDlSeasonDict {
		if season > 0 {
			seasonSet[season] = struct{}{}
		}
	}
	if len(seasonSet) == 0 {
		for _, episode := range seriesInfo.NeedDlEpsKeyList {
			if episode.Season > 0 {
				seasonSet[episode.Season] = struct{}{}
			}
		}
	}
	seasons := make([]int, 0, len(seasonSet))
	for season := range seasonSet {
		seasons = append(seasons, season)
	}
	sort.Ints(seasons)
	return seasons
}

func subHDTitleMatchesSeason(title string, season int) bool {
	if season <= 0 {
		return false
	}
	upper := strings.ToUpper(strings.Join(strings.Fields(title), " "))
	seasonTokens := []string{
		fmt.Sprintf("第%d季", season),
		"第" + zh.Uint64(season).String() + "季",
		fmt.Sprintf("SEASON %d", season),
		fmt.Sprintf("SEASON %02d", season),
	}
	for _, token := range seasonTokens {
		if strings.Contains(upper, strings.ToUpper(token)) {
			return true
		}
	}
	sToken := regexp.MustCompile(fmt.Sprintf(`(?:^|[^A-Z0-9])S0*%d(?:[^0-9]|$)`, season))
	return sToken.MatchString(upper)
}

func subHDTitleLooksLikeCollection(title string) bool {
	lower := strings.ToLower(title)
	for _, marker := range []string{"合集", "全集", "全季", "complete", "batch", "collection", "season pack"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return subHDCollectionRange.MatchString(title)
}
