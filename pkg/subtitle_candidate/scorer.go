package subtitle_candidate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
)

// EpisodeTarget describes one local episode. Zero AbsoluteEpisode means that
// no authoritative alternate-numbering mapping was available.
type EpisodeTarget struct {
	Season          int
	Episode         int
	AbsoluteEpisode int
}

// Target contains deterministic facts known about the local media.
type Target struct {
	Titles   []string
	Episodes []EpisodeTarget
}

var airedEpisodePattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s0*([0-9]{1,3})[ ._-]*e0*([0-9]{1,4})(?:[^0-9]|$)`)

// Rank computes a comparable score and a deterministic order. Supplier scores
// are retained separately because their scales are not comparable across sites.
func Rank(items []supplier.SubInfo, target Target) []supplier.SubInfo {
	ranked := append([]supplier.SubInfo(nil), items...)
	for i := range ranked {
		ranked[i].SourceScore = ranked[i].Score
		ranked[i].Score, ranked[i].ScoreReasons = score(ranked[i], target)
		ranked[i].MatchRank = 0
	}

	sortRanked(ranked)
	return ranked
}

// RankWithAmbiguityResolver lets an explicitly configured AI resolver break a
// close tie. It never sends more than five non-negative candidates, rejects
// invented IDs, and cannot promote a candidate outside the ambiguity window.
func RankWithAmbiguityResolver(ctx context.Context, items []supplier.SubInfo, target Target, resolver episode_identity.AmbiguityResolver) ([]supplier.SubInfo, episode_identity.AmbiguityResult, error) {
	ranked := Rank(items, target)
	abstain := episode_identity.AmbiguityResult{SchemaVersion: episode_identity.AmbiguitySchemaVersion, Decision: episode_identity.AmbiguityAbstain}
	if resolver == nil || len(ranked) < 2 || ranked[0].Score < 0 {
		return ranked, abstain, nil
	}

	const ambiguityWindow int64 = 150
	candidates := make([]episode_identity.CandidateFact, 0, 5)
	candidateIndexes := make(map[string]int, 5)
	for i := range ranked {
		if len(candidates) == cap(candidates) || ranked[i].Score < 0 || ranked[0].Score-ranked[i].Score > ambiguityWindow {
			break
		}
		id := candidateID(ranked[i])
		if _, duplicate := candidateIndexes[id]; duplicate {
			continue
		}
		candidateIndexes[id] = i
		candidates = append(candidates, episode_identity.CandidateFact{
			CandidateID: id, Supplier: ranked[i].FromWhere, Name: ranked[i].Name,
			Season: ranked[i].Season, Episode: ranked[i].Episode, AbsoluteEpisode: ranked[i].AbsoluteEpisode,
			DeterministicScore: ranked[i].Score, Evidence: append([]string(nil), ranked[i].ScoreReasons...),
		})
	}
	if len(candidates) < 2 {
		return ranked, abstain, nil
	}

	media := episode_identity.Request{Aliases: append([]string(nil), target.Titles...)}
	if len(target.Titles) > 0 {
		media.SeriesName = target.Titles[0]
	}
	if len(target.Episodes) == 1 {
		media.Season, media.Episode = target.Episodes[0].Season, target.Episodes[0].Episode
		media.AbsoluteEpisode = target.Episodes[0].AbsoluteEpisode
	}
	request := episode_identity.AmbiguityRequest{
		SchemaVersion: episode_identity.AmbiguitySchemaVersion,
		Media:         media,
		Candidates:    candidates,
	}
	result, err := resolver.ResolveAmbiguity(ctx, request)
	if err != nil {
		return ranked, abstain, err
	}
	if err := episode_identity.ValidateAmbiguityResult(request, result); err != nil {
		return ranked, abstain, err
	}
	if result.Decision != episode_identity.AmbiguityMatch {
		return ranked, result, nil
	}
	if result.Confidence < 0.85 {
		result.Decision = episode_identity.AmbiguityAbstain
		result.CandidateID = ""
		result.Evidence = append(result.Evidence, "below_selection_confidence:0.85")
		return ranked, result, nil
	}

	selectedIndex := candidateIndexes[result.CandidateID]
	bonus := ranked[0].Score - ranked[selectedIndex].Score + 1
	ranked[selectedIndex].Score += bonus
	ranked[selectedIndex].ScoreReasons = append(ranked[selectedIndex].ScoreReasons,
		fmt.Sprintf("ai_ambiguity:%s:%.2f:+%d", result.Model, result.Confidence, bonus))
	sortRanked(ranked)
	return ranked, result, nil
}

func sortRanked(ranked []supplier.SubInfo) {
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		leftSite, rightSite := strings.ToLower(ranked[i].FromWhere), strings.ToLower(ranked[j].FromWhere)
		if leftSite != rightSite {
			return leftSite < rightSite
		}
		if normalizedTopN(ranked[i].TopN) != normalizedTopN(ranked[j].TopN) {
			return normalizedTopN(ranked[i].TopN) < normalizedTopN(ranked[j].TopN)
		}
		leftName, rightName := strings.ToLower(ranked[i].Name), strings.ToLower(ranked[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return ranked[i].FileUrl < ranked[j].FileUrl
	})
	for i := range ranked {
		ranked[i].MatchRank = i + 1
	}
}

func candidateID(item supplier.SubInfo) string {
	raw := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d", strings.ToLower(item.FromWhere), item.FileUrl,
		item.Name, item.Season, item.Episode, item.AbsoluteEpisode)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:12])
}

func score(item supplier.SubInfo, target Target) (int64, []string) {
	var total int64
	reasons := make([]string, 0, 5)
	name := filepath.Base(item.Name)

	episodeScore, episodeReason := scoreEpisode(item, name, target.Episodes)
	if episodeScore != 0 {
		total += episodeScore
		reasons = append(reasons, episodeReason)
	}

	if overlap := titleTokenOverlap(name, target.Titles); overlap > 0 {
		points := int64(overlap * 30)
		if points > 180 {
			points = 180
		}
		total += points
		reasons = append(reasons, fmt.Sprintf("title_tokens:%d:+%d", overlap, points))
	}

	if item.TopN >= 0 {
		// Supplier ranks are historically zero-based ([site]_0 is Top 1).
		bonus := int64(100 - item.TopN*10)
		if bonus < 10 {
			bonus = 10
		}
		if bonus > 100 {
			bonus = 100
		}
		total += bonus
		reasons = append(reasons, fmt.Sprintf("source_rank:%d:+%d", item.TopN, bonus))
	}

	if len(reasons) == 0 {
		reasons = append(reasons, "no_positive_identity_evidence")
	}
	return total, reasons
}

func scoreEpisode(item supplier.SubInfo, name string, targets []EpisodeTarget) (int64, string) {
	best := int64(0)
	reason := ""
	knownNumbering := item.Season >= 0 && item.Episode > 0
	knownSeason := item.Season >= 0 && (item.Episode > 0 || item.IsFullSeason)
	if len(targets) > 0 && knownSeason {
		metadataMatches := false
		for _, target := range targets {
			if item.Season == target.Season && (item.IsFullSeason || item.Episode == target.Episode) {
				metadataMatches = true
				break
			}
		}
		if !metadataMatches {
			return -1200, fmt.Sprintf("numbering_mismatch:S%02dE%02d:-1200", item.Season, item.Episode)
		}
	}
	for _, target := range targets {
		candidateScore, candidateReason := int64(0), ""
		switch {
		case knownSeason && item.IsFullSeason && item.Season == target.Season:
			candidateScore, candidateReason = 700, fmt.Sprintf("full_season:S%02d:+700", target.Season)
		case knownNumbering && item.Season == target.Season && item.Episode == target.Episode:
			candidateScore, candidateReason = 1000, fmt.Sprintf("episode_metadata:S%02dE%02d:+1000", target.Season, target.Episode)
		case filenameContainsAiredEpisode(name, target.Season, target.Episode):
			candidateScore, candidateReason = 900, fmt.Sprintf("episode_filename:S%02dE%02d:+900", target.Season, target.Episode)
		case target.AbsoluteEpisode > 0 && item.AbsoluteEpisode == target.AbsoluteEpisode:
			candidateScore, candidateReason = 950, fmt.Sprintf("absolute_metadata:%d:+950", target.AbsoluteEpisode)
		case target.AbsoluteEpisode > 0 && episode_identity.FilenameContainsAbsoluteEpisode(name, target.AbsoluteEpisode):
			candidateScore, candidateReason = 850, fmt.Sprintf("absolute_filename:%d:+850", target.AbsoluteEpisode)
		}
		if candidateScore > best {
			best, reason = candidateScore, candidateReason
		}
	}

	return best, reason
}

func filenameContainsAiredEpisode(name string, season, episode int) bool {
	for _, match := range airedEpisodePattern.FindAllStringSubmatch(name, -1) {
		gotSeason, seasonErr := strconv.Atoi(match[1])
		gotEpisode, episodeErr := strconv.Atoi(match[2])
		if seasonErr == nil && episodeErr == nil && gotSeason == season && gotEpisode == episode {
			return true
		}
	}
	return false
}

func titleTokenOverlap(name string, titles []string) int {
	nameTokens := tokenSet(name)
	best := 0
	for _, title := range titles {
		count := 0
		for token := range tokenSet(title) {
			if _, ok := nameTokens[token]; ok {
				count++
			}
		}
		if count > best {
			best = count
		}
	}
	return best
}

func tokenSet(value string) map[string]struct{} {
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if len([]rune(part)) < 2 || isOnlyDigits(part) {
			continue
		}
		out[part] = struct{}{}
	}
	return out
}

func isOnlyDigits(value string) bool {
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return value != ""
}

func normalizedTopN(value int64) int64 {
	if value < 0 {
		return 1<<62 - 1
	}
	return value
}
