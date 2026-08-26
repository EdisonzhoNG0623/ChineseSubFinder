package sub_helper

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/decode"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/series"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
	"github.com/Tnze/go.num/v2/zh"
	"golang.org/x/text/width"
)

var (
	xEpisodeToken        = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]])0*([0-9]{1,3})X0*([0-9]{1,4})(?:[^[:alnum:]]|$)`)
	seasonPathToken      = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]])(?:S|SEASON)[ ._-]*0*([0-9]{1,3})(?:[^[:alnum:]]|$)`)
	episodeToken         = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]])(?:E|EP|EPISODE)[ ._-]*0*([0-9]{1,4})(?:[^[:alnum:]]|$)`)
	chineseEpisode       = regexp.MustCompile(`第[[:space:]]*0*([0-9]{1,4})[[:space:]]*[集話话]`)
	chineseNumberSeason  = regexp.MustCompile(`第([零〇一二两三四五六七八九十百千]+)季`)
	chineseNumberEpisode = regexp.MustCompile(`第([零〇一二两三四五六七八九十百千]+)[集話话]`)
	bareNumberToken      = regexp.MustCompile(`[0-9]{1,4}`)
)

type seriesEpisodeResolver struct {
	byAired    map[string]series.EpisodeInfo
	byEpisode  map[int][]series.EpisodeInfo
	byAbsolute map[int][]series.EpisodeInfo
}

func newSeriesEpisodeResolver(info *series.SeriesInfo) seriesEpisodeResolver {
	resolver := seriesEpisodeResolver{
		byAired:    make(map[string]series.EpisodeInfo),
		byEpisode:  make(map[int][]series.EpisodeInfo),
		byAbsolute: make(map[int][]series.EpisodeInfo),
	}
	if info == nil {
		return resolver
	}
	for _, episode := range info.EpList {
		resolver.add(episode)
	}
	// Emby and queue-scoped reads may populate only NeedDlEpsKeyList. Merge it
	// so collection parsing still has a safe local inventory in those modes.
	for _, episode := range info.NeedDlEpsKeyList {
		resolver.add(episode)
	}
	for _, episode := range info.ArchiveEpList {
		resolver.add(episode)
	}
	return resolver
}

func (r seriesEpisodeResolver) add(episode series.EpisodeInfo) {
	if episode.Season <= 0 || episode.Episode <= 0 {
		return
	}
	key := pkg.GetEpisodeKeyName(episode.Season, episode.Episode)
	if _, exists := r.byAired[key]; exists {
		return
	}
	r.byAired[key] = episode
	r.byEpisode[episode.Episode] = append(r.byEpisode[episode.Episode], episode)
	if episode.AbsoluteEpisode > 0 {
		r.byAbsolute[episode.AbsoluteEpisode] = append(r.byAbsolute[episode.AbsoluteEpisode], episode)
	}
}

// Resolve maps an extracted subtitle path to one and only one local episode.
// Weak bare-number formats are accepted only for a collection and only when
// the number uniquely identifies an episode in the local series inventory.
func (r seriesEpisodeResolver) Resolve(relativePath string, source supplier.SubInfo) (int, int, bool) {
	normalized := width.Fold.String(filepath.ToSlash(relativePath))
	if season, episode, ok := parseExplicitSeasonEpisode(normalized); ok {
		return r.acceptExplicit(season, episode)
	}

	seasonHint := source.Season
	if pathSeason, ok := parseFirstNumber(seasonPathToken, normalized); ok {
		seasonHint = pathSeason
	} else if pathSeason, ok = parseChineseNumber(chineseNumberSeason, normalized); ok {
		seasonHint = pathSeason
	}
	if episode, ok := parseFirstNumber(episodeToken, normalized); ok {
		return r.resolveNumber(episode, seasonHint)
	}
	if episode, ok := parseFirstNumber(chineseEpisode, normalized); ok {
		return r.resolveNumber(episode, seasonHint)
	}
	if episode, ok := parseChineseNumber(chineseNumberEpisode, normalized); ok {
		return r.resolveNumber(episode, seasonHint)
	}

	if source.Season > 0 && source.Episode > 0 && source.AbsoluteEpisode > 0 &&
		filenameContainsNumber(normalized, source.AbsoluteEpisode) {
		return source.Season, source.Episode, true
	}
	if source.Season > 0 && source.Episode > 0 {
		base := strings.TrimSuffix(filepath.Base(normalized), filepath.Ext(normalized))
		if len(parseAllNumbers(bareNumberToken, base)) == 0 {
			return r.acceptExplicit(source.Season, source.Episode)
		}
	}

	collectionScoped := source.IsFullSeason || (source.Season > 0 && source.Episode == 0)
	if !collectionScoped || len(r.byAired) == 0 {
		return 0, 0, false
	}

	base := strings.TrimSuffix(filepath.Base(normalized), filepath.Ext(normalized))
	numbers := parseAllNumbers(bareNumberToken, base)
	resolved := make(map[string]series.EpisodeInfo)
	for _, number := range numbers {
		for _, candidate := range r.numberCandidates(number, seasonHint) {
			resolved[pkg.GetEpisodeKeyName(candidate.Season, candidate.Episode)] = candidate
		}
	}
	if len(resolved) != 1 {
		return 0, 0, false
	}
	for _, episode := range resolved {
		return episode.Season, episode.Episode, true
	}
	return 0, 0, false
}

func (r seriesEpisodeResolver) acceptExplicit(season, episode int) (int, int, bool) {
	if len(r.byAired) == 0 {
		return season, episode, season > 0 && episode > 0
	}
	if _, exists := r.byAired[pkg.GetEpisodeKeyName(season, episode)]; !exists {
		return 0, 0, false
	}
	return season, episode, true
}

func (r seriesEpisodeResolver) resolveNumber(number, seasonHint int) (int, int, bool) {
	if len(r.byAired) == 0 {
		if seasonHint > 0 && number > 0 {
			return seasonHint, number, true
		}
		return 0, 0, false
	}
	candidates := r.numberCandidates(number, seasonHint)
	unique := make(map[string]series.EpisodeInfo)
	for _, candidate := range candidates {
		unique[pkg.GetEpisodeKeyName(candidate.Season, candidate.Episode)] = candidate
	}
	if len(unique) != 1 {
		return 0, 0, false
	}
	for _, candidate := range unique {
		return candidate.Season, candidate.Episode, true
	}
	return 0, 0, false
}

func (r seriesEpisodeResolver) numberCandidates(number, seasonHint int) []series.EpisodeInfo {
	candidates := make([]series.EpisodeInfo, 0, 2)
	for _, episode := range r.byEpisode[number] {
		if seasonHint <= 0 || episode.Season == seasonHint {
			candidates = append(candidates, episode)
		}
	}
	for _, episode := range r.byAbsolute[number] {
		if seasonHint <= 0 || episode.Season == seasonHint {
			candidates = append(candidates, episode)
		}
	}
	return candidates
}

func parseExplicitSeasonEpisode(value string) (int, int, bool) {
	_, season, episode, err := decode.GetSeasonAndEpisodeFromSubFileName(value)
	if err == nil && season > 0 && episode > 0 {
		return season, episode, true
	}
	match := xEpisodeToken.FindStringSubmatch(value)
	if len(match) == 3 {
		parsedSeason, seasonErr := strconv.Atoi(match[1])
		parsedEpisode, episodeErr := strconv.Atoi(match[2])
		if seasonErr == nil && episodeErr == nil && parsedSeason > 0 && parsedEpisode > 0 {
			return parsedSeason, parsedEpisode, true
		}
	}
	return 0, 0, false
}

func parseFirstNumber(expression *regexp.Regexp, value string) (int, bool) {
	match := expression.FindStringSubmatch(value)
	if len(match) != 2 {
		return 0, false
	}
	number, err := strconv.Atoi(match[1])
	return number, err == nil && number > 0
}

func parseAllNumbers(expression *regexp.Regexp, value string) []int {
	matches := expression.FindAllStringIndex(value, -1)
	numbers := make([]int, 0, len(matches))
	for _, match := range matches {
		if len(match) != 2 || (match[0] > 0 && isASCIIAlphaNumeric(value[match[0]-1])) ||
			(match[1] < len(value) && isASCIIAlphaNumeric(value[match[1]])) {
			continue
		}
		number, err := strconv.Atoi(value[match[0]:match[1]])
		if err == nil && number > 0 {
			numbers = append(numbers, number)
		}
	}
	return numbers
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func parseChineseNumber(expression *regexp.Regexp, value string) (int, bool) {
	match := expression.FindStringSubmatch(value)
	if len(match) != 2 {
		return 0, false
	}
	text := strings.NewReplacer("两", "二", "〇", "零").Replace(match[1])
	var number zh.Uint64
	if _, err := fmt.Sscan(text, &number); err != nil || number == 0 || number > 9999 {
		return 0, false
	}
	return int(number), true
}

func filenameContainsNumber(value string, number int) bool {
	for _, candidate := range parseAllNumbers(bareNumberToken, value) {
		if candidate == number {
			return true
		}
	}
	return false
}
