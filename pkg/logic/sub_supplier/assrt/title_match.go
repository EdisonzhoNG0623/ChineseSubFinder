package assrt

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/ChineseSubFinder/ChineseSubFinder/internal/models"
)

var (
	seasonEpisodeToken = regexp.MustCompile(`^s\d{1,3}(?:e\d{1,4})?$`)
	episodeToken       = regexp.MustCompile(`^e\d{1,4}$`)
	xEpisodeToken      = regexp.MustCompile(`^\d{1,3}x\d{1,4}$`)
)

func assrtCandidateFieldsMatchMedia(mediaInfo *models.MediaInfo, targetEpisode int, values ...string) (bool, string) {
	return assrtCandidateFieldsMatchMediaForEpisodes(mediaInfo, []int{targetEpisode}, values...)
}

func assrtCandidateFieldsMatchMediaForEpisodes(mediaInfo *models.MediaInfo, targetEpisodes []int, values ...string) (bool, string) {
	matchedField := false
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		matchedField = true
		if !assrtCandidateMatchesMediaForEpisodes(mediaInfo, value, targetEpisodes) {
			return false, value
		}
	}
	return matchedField, ""
}

// assrtCandidateMatchesMedia rejects search hits whose subtitle filename is for
// another title. ASSRT can return unrelated results that only share SxxExx with
// the query, so season and episode matching alone is not sufficient.
func assrtCandidateMatchesMedia(mediaInfo *models.MediaInfo, candidate string) bool {
	return assrtCandidateMatchesMediaForEpisode(mediaInfo, candidate, 0)
}

func assrtCandidateMatchesMediaForEpisode(mediaInfo *models.MediaInfo, candidate string, targetEpisode int) bool {
	return assrtCandidateMatchesMediaForEpisodes(mediaInfo, candidate, []int{targetEpisode})
}

func assrtCandidateMatchesMediaForEpisodes(mediaInfo *models.MediaInfo, candidate string, targetEpisodes []int) bool {
	candidateTokens := titleTokens(candidate)
	if len(candidateTokens) == 0 {
		return false
	}

	targetYear := mediaYear(mediaInfo.Year)
	aliases := make([][]string, 0, 3)
	for _, alias := range []string{mediaInfo.TitleCn, mediaInfo.TitleEn, mediaInfo.OriginalTitle} {
		aliasTokens := titleTokens(alias)
		if len(aliasTokens) != 0 {
			aliases = append(aliases, aliasTokens)
		}
	}
	for _, aliasTokens := range aliases {
		if candidateContainsAlias(candidateTokens, aliasTokens, aliases, targetYear, targetEpisodes) {
			return true
		}
	}

	return false
}

func candidateContainsAlias(candidate, alias []string, aliases [][]string, targetYear string, targetEpisodes []int) bool {
	for start := 0; start+len(alias) <= len(candidate); start++ {
		if !equalTokens(candidate[start:start+len(alias)], alias) {
			continue
		}

		next := skipAdjacentAliases(candidate, start+len(alias), aliases)
		if next == len(candidate) || isReleaseMetadataAt(candidate, next, targetYear) {
			return true
		}
		if episodeRangeContainsAny(candidate, next, targetEpisodes) {
			return true
		}
	}

	return false
}

func episodeRangeContainsAny(tokens []string, index int, targetEpisodes []int) bool {
	for _, targetEpisode := range targetEpisodes {
		if episodeRangeContains(tokens, index, targetEpisode) {
			return true
		}
	}
	return false
}

func skipAdjacentAliases(candidate []string, index int, aliases [][]string) int {
	for index < len(candidate) {
		matched := false
		for _, alias := range aliases {
			if len(alias) == 0 || index+len(alias) > len(candidate) {
				continue
			}
			if equalTokens(candidate[index:index+len(alias)], alias) {
				index += len(alias)
				matched = true
				break
			}
		}
		if !matched {
			break
		}
	}
	return index
}

func episodeRangeContains(tokens []string, index, targetEpisode int) bool {
	if targetEpisode <= 0 || index >= len(tokens) {
		return false
	}
	start, ok := episodeNumber(tokens[index])
	if !ok {
		return false
	}
	if start == targetEpisode {
		return true
	}
	if index+1 >= len(tokens) {
		return false
	}
	end, ok := episodeNumber(tokens[index+1])
	return ok && start <= targetEpisode && targetEpisode <= end
}

func episodeNumber(token string) (int, bool) {
	if token == "" || len(token) > 4 || !isDigits(token) {
		return 0, false
	}
	value, err := strconv.Atoi(token)
	if err != nil || value <= 0 || value >= 1900 {
		return 0, false
	}
	return value, true
}

func equalTokens(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func isReleaseMetadataAt(tokens []string, index int, targetYear string) bool {
	if isReleaseMetadata(tokens[index], targetYear) {
		return true
	}
	if tokens[index] == "第" && index+2 < len(tokens) && isDigits(tokens[index+1]) &&
		(tokens[index+2] == "季" || tokens[index+2] == "集") {
		return true
	}
	return false
}

func isDigits(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isReleaseMetadata(token, targetYear string) bool {
	if seasonEpisodeToken.MatchString(token) || episodeToken.MatchString(token) || xEpisodeToken.MatchString(token) {
		return true
	}
	if strings.HasPrefix(token, "第") && (strings.HasSuffix(token, "季") || strings.HasSuffix(token, "集")) {
		return true
	}
	if len(token) == 4 && token[0] >= '1' && token[0] <= '2' {
		return targetYear != "" && token == targetYear
	}

	switch token {
	case "bluray", "bdrip", "dvdrip", "hdtv", "web", "webdl", "webrip", "remux", "720p", "1080p", "2160p", "4k",
		"zip", "rar", "7z", "srt", "ass", "ssa", "sup", "vtt",
		"chs", "cht", "eng", "zh", "cn", "gb", "big5", "simplified", "traditional", "简体", "繁体", "中文", "双语":
		return true
	default:
		return false
	}
}

func mediaYear(year string) string {
	for _, token := range titleTokens(year) {
		if len(token) == 4 && token[0] >= '1' && token[0] <= '2' {
			return token
		}
	}
	return ""
}

func titleTokens(value string) []string {
	var tokens []string
	var current []rune
	currentHan := false

	flush := func() {
		if len(current) == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(string(current)))
		current = current[:0]
	}

	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}

		nowHan := unicode.Is(unicode.Han, r)
		if len(current) > 0 && nowHan != currentHan {
			flush()
		}
		currentHan = nowHan
		current = append(current, unicode.ToLower(r))
	}
	flush()

	return tokens
}
