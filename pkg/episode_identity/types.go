package episode_identity

import "context"

// ExternalIDs are stable identifiers supplied by media metadata providers.
// Empty fields are unknown; resolvers must never invent identifiers.
type ExternalIDs struct {
	IMDb  string `json:"imdb,omitempty"`
	TMDB  string `json:"tmdb,omitempty"`
	TVDB  string `json:"tvdb,omitempty"`
	AniDB string `json:"anidb,omitempty"`
}

type Request struct {
	IDs             ExternalIDs `json:"ids"`
	SeriesName      string      `json:"series_name,omitempty"`
	Aliases         []string    `json:"aliases,omitempty"`
	Season          int         `json:"season,omitempty"`
	Episode         int         `json:"episode,omitempty"`
	AbsoluteEpisode int         `json:"absolute_episode,omitempty"`
	EpisodeTitle    string      `json:"episode_title,omitempty"`
	AirDate         string      `json:"air_date,omitempty"`
	FileName        string      `json:"file_name,omitempty"`
}

type Evidence struct {
	Source     string  `json:"source"`
	Rule       string  `json:"rule"`
	Confidence float64 `json:"confidence"`
}

// Identity keeps aired numbering as the canonical local destination while
// exposing alternate orders for supplier-specific searches.
type Identity struct {
	IDs             ExternalIDs
	Season          int
	Episode         int
	AbsoluteEpisode int
	SceneSeason     int
	SceneEpisode    int
	Confidence      float64
	Evidence        []Evidence
}

type Resolver interface {
	Resolve(ctx context.Context, request Request) (Identity, error)
}

// CandidateResolver exposes bounded deterministic alternatives when a data
// source contains conflicting mappings. Callers may resolve the ambiguity with
// another guarded mechanism, but must never accept an identity outside this
// candidate set.
type CandidateResolver interface {
	ResolveCandidates(ctx context.Context, request Request) ([]Identity, error)
}

// SeriesMatcher reports whether a resolver recognizes a series even when it
// cannot map the caller's custom season split to one absolute episode.
type SeriesMatcher interface {
	MatchesSeries(ctx context.Context, request Request) (bool, error)
}

// ResolverFunc makes deterministic and test resolvers easy to inject without
// coupling suppliers to a concrete metadata service.
type ResolverFunc func(context.Context, Request) (Identity, error)

func (f ResolverFunc) Resolve(ctx context.Context, request Request) (Identity, error) {
	return f(ctx, request)
}
