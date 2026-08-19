package subtitle_candidate

import (
	"context"
	"reflect"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/supplier"
)

func TestRankPrefersExactAiredThenAbsoluteAndIsDeterministic(t *testing.T) {
	items := []supplier.SubInfo{
		{FromWhere: "z", TopN: 1, Name: "Fairy.Tail.288.zh.srt", Season: -1, Episode: -1},
		{FromWhere: "a", TopN: 1, Name: "Fairy.Tail.S08E11.zh.srt", Season: 8, Episode: 11},
		{FromWhere: "a", TopN: 2, Name: "Fairy.Tail.S08E10.zh.srt", Season: 8, Episode: 10},
	}
	target := Target{Titles: []string{"Fairy Tail"}, Episodes: []EpisodeTarget{{Season: 8, Episode: 11, AbsoluteEpisode: 288}}}

	got := Rank(items, target)
	if got[0].Name != "Fairy.Tail.S08E11.zh.srt" || got[1].Name != "Fairy.Tail.288.zh.srt" {
		t.Fatalf("unexpected order: %#v", []string{got[0].Name, got[1].Name, got[2].Name})
	}
	if got[2].Score >= 0 {
		t.Fatalf("wrong numbered episode should be penalized, got %d", got[2].Score)
	}
	if got[0].SourceScore != 0 || got[0].MatchRank != 1 || len(got[0].ScoreReasons) == 0 {
		t.Fatalf("missing audit metadata: %#v", got[0])
	}

	again := Rank(items, target)
	if !reflect.DeepEqual(got, again) {
		t.Fatal("ranking must be deterministic")
	}
}

func TestRankPreservesSupplierScoreSeparately(t *testing.T) {
	got := Rank([]supplier.SubInfo{{FromWhere: "site", TopN: 1, Name: "Movie.2024.zh.srt", Score: 73}}, Target{Titles: []string{"Movie"}})
	if got[0].SourceScore != 73 {
		t.Fatalf("source score lost: %#v", got[0])
	}
	if got[0].Score == 73 {
		t.Fatalf("unified score should be independently computed: %#v", got[0])
	}
}

func TestRankTreatsSupplierTopNAsZeroBased(t *testing.T) {
	got := Rank([]supplier.SubInfo{
		{FromWhere: "site", TopN: 1, Name: "Movie.second.srt", Season: -1, Episode: -1},
		{FromWhere: "site", TopN: 0, Name: "Movie.first.srt", Season: -1, Episode: -1},
	}, Target{Titles: []string{"Movie"}})
	if got[0].TopN != 0 {
		t.Fatalf("zero-based TopN=0 must remain the best supplier rank: %#v", got)
	}
}

func TestRankRejectsConflictingMetadataEvenWhenFilenameLooksRight(t *testing.T) {
	got := Rank([]supplier.SubInfo{{
		FromWhere: "site", TopN: 0, Name: "Fairy.Tail.S08E11.288.srt",
		Season: 8, Episode: 10, AbsoluteEpisode: 288,
	}}, Target{Titles: []string{"Fairy Tail"}, Episodes: []EpisodeTarget{{Season: 8, Episode: 11, AbsoluteEpisode: 288}}})
	if got[0].Score >= 0 || got[0].ScoreReasons[0] != "numbering_mismatch:S08E10:-1200" {
		t.Fatalf("conflicting structured metadata must win over filename hints: %#v", got[0])
	}
}

func TestRankWithAmbiguityResolverOnlyBreaksCloseTie(t *testing.T) {
	items := []supplier.SubInfo{
		{FromWhere: "a", TopN: 1, Name: "Movie.zh.srt", FileUrl: "a"},
		{FromWhere: "b", TopN: 2, Name: "Movie.zh.srt", FileUrl: "b"},
	}
	resolver := episode_identity.AmbiguityResolverFunc(func(_ context.Context, request episode_identity.AmbiguityRequest) (episode_identity.AmbiguityResult, error) {
		return episode_identity.AmbiguityResult{
			SchemaVersion: episode_identity.AmbiguitySchemaVersion,
			Decision:      episode_identity.AmbiguityMatch,
			CandidateID:   request.Candidates[1].CandidateID,
			Confidence:    .95,
			Model:         "test-model",
			ModelVersion:  "1",
		}, nil
	})

	got, decision, err := RankWithAmbiguityResolver(context.Background(), items, Target{Titles: []string{"Movie"}}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != episode_identity.AmbiguityMatch || got[0].FromWhere != "b" {
		t.Fatalf("AI should only break the close tie: %#v, %#v", decision, got)
	}
	if got[0].ScoreReasons[len(got[0].ScoreReasons)-1] != "ai_ambiguity:test-model:0.95:+11" {
		t.Fatalf("AI decision not audited: %#v", got[0].ScoreReasons)
	}
}

func TestRankWithAmbiguityResolverIgnoresLowConfidenceMatch(t *testing.T) {
	items := []supplier.SubInfo{
		{FromWhere: "a", TopN: 1, Name: "Movie.zh.srt", FileUrl: "a"},
		{FromWhere: "b", TopN: 2, Name: "Movie.zh.srt", FileUrl: "b"},
	}
	resolver := episode_identity.AmbiguityResolverFunc(func(_ context.Context, request episode_identity.AmbiguityRequest) (episode_identity.AmbiguityResult, error) {
		return episode_identity.AmbiguityResult{
			SchemaVersion: episode_identity.AmbiguitySchemaVersion,
			Decision:      episode_identity.AmbiguityMatch,
			CandidateID:   request.Candidates[1].CandidateID,
			Confidence:    .6,
			Model:         "test-model",
			ModelVersion:  "1",
		}, nil
	})

	got, decision, err := RankWithAmbiguityResolver(context.Background(), items, Target{Titles: []string{"Movie"}}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != episode_identity.AmbiguityAbstain || got[0].FromWhere != "a" {
		t.Fatalf("low confidence must preserve deterministic ranking: %#v, %#v", decision, got)
	}
}
