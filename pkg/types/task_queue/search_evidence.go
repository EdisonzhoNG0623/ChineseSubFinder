package task_queue

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
)

const SearchEvidenceVersion = 2

// SearchEvidenceFingerprint produces a versioned, path-free identity for the
// episode facts that affect supplier queries. It is safe to persist and expose
// in diagnostics: no library path, credential, provider URL, or raw query is
// included.
func SearchEvidenceFingerprint(seriesName string, season, episode, absoluteEpisode,
	sceneSeason, sceneEpisode int, numberingSource string) string {
	return SearchEvidenceFingerprintWithAliases(seriesName, nil, season, episode, absoluteEpisode,
		sceneSeason, sceneEpisode, numberingSource)
}

// SearchEvidenceFingerprintWithAliases includes every normalized title that
// can materially change the supplier query plan. Only the digest is exposed;
// directory layout and raw query strings never enter diagnostics.
func SearchEvidenceFingerprintWithAliases(seriesName string, aliases []string, season, episode, absoluteEpisode,
	sceneSeason, sceneEpisode int, numberingSource string) string {
	seriesName = normalizeSearchAlias(seriesName)
	aliases = NormalizeSearchAliases(aliases...)
	filtered := aliases[:0]
	for _, alias := range aliases {
		if normalizeSearchAlias(alias) != seriesName {
			filtered = append(filtered, alias)
		}
	}
	raw := fmt.Sprintf("v2\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d\x00%s",
		seriesName, strings.Join(filtered, "\x1f"), season, episode,
		absoluteEpisode, sceneSeason, sceneEpisode,
		strings.ToLower(strings.TrimSpace(numberingSource)))
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:12])
}

// NormalizeSearchAliases matches series metadata normalization while keeping
// first-seen order, which is also the supplier fallback order.
func NormalizeSearchAliases(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		key := normalizeSearchAlias(value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeSearchAlias(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

// RefreshSearchFingerprint updates the current identity fingerprint for a
// series/anime task. Movies have no episode identity and keep this field empty.
func (j *OneJob) RefreshSearchFingerprint() string {
	if j == nil {
		return ""
	}
	if (j.VideoType != common.Series && j.VideoType != common.Anime) || j.Season <= 0 || j.Episode <= 0 {
		j.SearchFingerprint = ""
		j.SearchEvidenceVersion = 0
		return ""
	}
	j.SearchAliases = NormalizeSearchAliases(j.SearchAliases...)
	j.SearchEvidenceVersion = SearchEvidenceVersion
	j.SearchFingerprint = SearchEvidenceFingerprintWithAliases(j.SeriesName, j.SearchAliases, j.Season, j.Episode,
		j.AbsoluteEpisode, j.SceneSeason, j.SceneEpisode, j.NumberingSource)
	return j.SearchFingerprint
}
