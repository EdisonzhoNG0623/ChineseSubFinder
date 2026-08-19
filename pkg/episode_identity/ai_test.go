package episode_identity

import (
	"context"
	"testing"
)

func ambiguityTestRequest() AmbiguityRequest {
	return AmbiguityRequest{
		SchemaVersion: AmbiguitySchemaVersion,
		Candidates:    []CandidateFact{{CandidateID: "a"}, {CandidateID: "b"}},
	}
}

func TestDisabledAmbiguityResolverAlwaysAbstains(t *testing.T) {
	got, err := (DisabledAmbiguityResolver{}).ResolveAmbiguity(context.Background(), ambiguityTestRequest())
	if err != nil || got.Decision != AmbiguityAbstain || got.CandidateID != "" {
		t.Fatalf("unexpected disabled result: %#v, %v", got, err)
	}
}

func TestGuardedAmbiguityResolverRejectsInventedCandidate(t *testing.T) {
	resolver := GuardedAmbiguityResolver{
		Enabled: true,
		Backend: AmbiguityResolverFunc(func(context.Context, AmbiguityRequest) (AmbiguityResult, error) {
			return AmbiguityResult{SchemaVersion: AmbiguitySchemaVersion, Decision: AmbiguityMatch, CandidateID: "invented", Confidence: .99, Model: "test", ModelVersion: "1"}, nil
		}),
	}
	got, err := resolver.ResolveAmbiguity(context.Background(), ambiguityTestRequest())
	if err == nil || got.Decision != AmbiguityAbstain {
		t.Fatalf("invented candidate must fail closed: %#v, %v", got, err)
	}
}

func TestGuardedAmbiguityResolverAbstainsBelowConfidence(t *testing.T) {
	resolver := GuardedAmbiguityResolver{
		Enabled: true,
		Backend: AmbiguityResolverFunc(func(context.Context, AmbiguityRequest) (AmbiguityResult, error) {
			return AmbiguityResult{SchemaVersion: AmbiguitySchemaVersion, Decision: AmbiguityMatch, CandidateID: "a", Confidence: .7, Model: "test", ModelVersion: "1"}, nil
		}),
	}
	got, err := resolver.ResolveAmbiguity(context.Background(), ambiguityTestRequest())
	if err != nil || got.Decision != AmbiguityAbstain || got.CandidateID != "" {
		t.Fatalf("low confidence must abstain: %#v, %v", got, err)
	}
}
