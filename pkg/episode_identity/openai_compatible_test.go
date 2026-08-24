package episode_identity

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestOpenAICompatibleResolverValidatesDecision(t *testing.T) {
	resolver, err := NewOpenAICompatibleResolver(OpenAICompatibleConfig{BaseURL: "http://ai.test/v1", APIKey: "test-key", Model: "test-model", Timeout: time.Second, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	resolver.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("authorization header missing")
		}
		body := `{"model":"test-model-v1","choices":[{"message":{"role":"assistant","content":"{\"schema_version\":\"1\",\"decision\":\"MATCH\",\"candidate_id\":\"a\",\"confidence\":0.96,\"evidence\":[\"aired episode agrees\"]}"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	got, err := resolver.ResolveAmbiguity(context.Background(), ambiguityTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != AmbiguityMatch || got.CandidateID != "a" || got.ModelVersion != "test-model-v1" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestOpenAICompatibleResolverAcceptsStringEvidence(t *testing.T) {
	resolver, err := NewOpenAICompatibleResolver(OpenAICompatibleConfig{BaseURL: "http://ai.test/v1", Model: "deepseek-v4-flash", Timeout: time.Second, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	resolver.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		// OpenCode Go's DeepSeek V4 Flash can return a single evidence string
		// even when prompted for strict JSON. Treat it as one evidence item.
		body := `{"model":"deepseek-v4-flash","choices":[{"message":{"role":"assistant","content":"{\"schema_version\":\"1\",\"decision\":\"MATCH\",\"candidate_id\":\"a\",\"confidence\":1,\"evidence\":\"episode number agrees\"}"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	got, err := resolver.ResolveAmbiguity(context.Background(), ambiguityTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Evidence) != 1 || got.Evidence[0] != "episode number agrees" {
		t.Fatalf("unexpected normalized evidence: %#v", got.Evidence)
	}
}

func TestOpenAICompatibleResolverRejectsInvalidEvidenceType(t *testing.T) {
	resolver, err := NewOpenAICompatibleResolver(OpenAICompatibleConfig{BaseURL: "http://ai.test/v1", Model: "test-model", Timeout: time.Second, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	resolver.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"{\"schema_version\":\"1\",\"decision\":\"MATCH\",\"candidate_id\":\"a\",\"confidence\":1,\"evidence\":{\"unsafe\":true}}"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	if _, err := resolver.ResolveAmbiguity(context.Background(), ambiguityTestRequest()); err == nil {
		t.Fatal("expected invalid evidence object to be rejected")
	}
}

func TestOpenAICompatibleResolverRejectsInsecureEndpointByDefault(t *testing.T) {
	if _, err := NewOpenAICompatibleResolver(OpenAICompatibleConfig{BaseURL: "http://127.0.0.1:11434/v1", Model: "local"}); err == nil {
		t.Fatal("expected insecure endpoint rejection")
	}
}

func TestOpenAICompatibleResolverRejectsCredentialsInURL(t *testing.T) {
	_, err := NewOpenAICompatibleResolver(OpenAICompatibleConfig{
		BaseURL: "https://user:password@ai.test/v1", Model: "test-model",
	})
	if err == nil {
		t.Fatal("expected URL credentials to be rejected")
	}
}
