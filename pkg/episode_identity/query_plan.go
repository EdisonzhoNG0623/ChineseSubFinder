package episode_identity

import (
	"fmt"
	"strings"
)

type QueryKind string

const (
	QueryAired    QueryKind = "AIRED"
	QueryAbsolute QueryKind = "ABSOLUTE"
	QueryScene    QueryKind = "SCENE"
)

type QueryVariant struct {
	Kind  QueryKind
	Query string
}

// BuildSearchPlan returns deterministic queries in decreasing precision.
// Exact aired/scene tokens precede absolute aliases; a bare absolute number is
// deliberately last because it has the greatest false-positive risk.
func BuildSearchPlan(aliases []string, identity Identity) []QueryVariant {
	plan := make([]QueryVariant, 0, len(aliases)*6)
	seen := make(map[string]struct{}, len(aliases)*6)
	appendQuery := func(kind QueryKind, query string) {
		query = strings.Join(strings.Fields(query), " ")
		if query == "" {
			return
		}
		key := strings.ToLower(query)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		plan = append(plan, QueryVariant{Kind: kind, Query: query})
	}

	aliases = uniqueAliases(aliases)
	for _, alias := range aliases {
		if identity.Season > 0 && identity.Episode > 0 {
			appendQuery(QueryAired, fmt.Sprintf("%s S%02dE%02d", alias, identity.Season, identity.Episode))
		}
	}
	for _, alias := range aliases {
		if identity.SceneSeason > 0 && identity.SceneEpisode > 0 &&
			(identity.SceneSeason != identity.Season || identity.SceneEpisode != identity.Episode) {
			appendQuery(QueryScene, fmt.Sprintf("%s S%02dE%02d", alias, identity.SceneSeason, identity.SceneEpisode))
		}
	}
	for _, alias := range aliases {
		if identity.AbsoluteEpisode > 0 {
			appendQuery(QueryAbsolute, fmt.Sprintf("%s E%d", alias, identity.AbsoluteEpisode))
		}
	}
	for _, alias := range aliases {
		if identity.AbsoluteEpisode > 0 {
			appendQuery(QueryAbsolute, fmt.Sprintf("%s EP%d", alias, identity.AbsoluteEpisode))
		}
	}
	for _, alias := range aliases {
		if identity.AbsoluteEpisode > 0 {
			appendQuery(QueryAbsolute, fmt.Sprintf("%s #%d", alias, identity.AbsoluteEpisode))
		}
	}
	for _, alias := range aliases {
		if identity.AbsoluteEpisode > 0 {
			appendQuery(QueryAbsolute, fmt.Sprintf("%s %d", alias, identity.AbsoluteEpisode))
		}
	}
	return plan
}

func uniqueAliases(aliases []string) []string {
	out := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.Join(strings.Fields(alias), " ")
		if alias == "" {
			continue
		}
		key := strings.ToLower(alias)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, alias)
	}
	return out
}
