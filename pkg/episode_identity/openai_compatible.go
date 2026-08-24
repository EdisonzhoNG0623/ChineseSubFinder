package episode_identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OpenAICompatibleConfig struct {
	BaseURL       string
	APIKey        string
	Model         string
	Timeout       time.Duration
	AllowInsecure bool
}

type OpenAICompatibleResolver struct {
	config OpenAICompatibleConfig
	client *http.Client
}

func NewOpenAICompatibleResolver(config OpenAICompatibleConfig) (*OpenAICompatibleResolver, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" || strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("AI base URL and model are required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("AI base URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("AI base URL must not contain credentials, query parameters, or fragments")
	}
	if parsed.Scheme == "http" && !config.AllowInsecure {
		return nil, errors.New("insecure AI endpoint is not allowed")
	}
	config.BaseURL = baseURL
	config.Model = strings.TrimSpace(config.Model)
	if config.Timeout < 3*time.Second || config.Timeout > 60*time.Second {
		config.Timeout = 20 * time.Second
	}
	client := &http.Client{Timeout: config.Timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return &OpenAICompatibleResolver{config: config, client: client}, nil
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func (r *OpenAICompatibleResolver) ResolveAmbiguity(ctx context.Context, request AmbiguityRequest) (AmbiguityResult, error) {
	if len(request.Candidates) < 2 || len(request.Candidates) > 8 {
		return AmbiguityResult{}, fmt.Errorf("AI ambiguity request must contain 2 to 8 candidates, got %d", len(request.Candidates))
	}
	request.Media.FileName = ""
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return AmbiguityResult{}, err
	}
	payload := chatCompletionRequest{
		Model: r.config.Model, Temperature: 0, MaxTokens: 300,
		Messages: []chatMessage{
			{Role: "system", Content: "You resolve subtitle ambiguity using only supplied facts. Return strict JSON with schema_version, decision, candidate_id, confidence, evidence. decision must be MATCH, NO_MATCH, or ABSTAIN. Never invent a candidate_id."},
			{Role: "user", Content: string(requestJSON)},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return AmbiguityResult{}, err
	}
	endpoint := r.config.BaseURL
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return AmbiguityResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if r.config.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	}
	response, err := r.client.Do(httpRequest)
	if err != nil {
		return AmbiguityResult{}, fmt.Errorf("AI request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AmbiguityResult{}, fmt.Errorf("AI request returned HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, 256*1024+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return AmbiguityResult{}, fmt.Errorf("read AI response: %w", err)
	}
	if len(responseBody) > 256*1024 {
		return AmbiguityResult{}, errors.New("AI response exceeded 256 KiB")
	}
	var completion chatCompletionResponse
	if err := json.Unmarshal(responseBody, &completion); err != nil {
		return AmbiguityResult{}, errors.New("AI response was not valid JSON")
	}
	if len(completion.Choices) != 1 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return AmbiguityResult{}, errors.New("AI response did not contain exactly one choice")
	}
	var result AmbiguityResult
	decoder := json.NewDecoder(strings.NewReader(completion.Choices[0].Message.Content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return AmbiguityResult{}, errors.New("AI decision was not valid strict JSON")
	}
	result.Model = r.config.Model
	result.ModelVersion = strings.TrimSpace(completion.Model)
	if result.ModelVersion == "" {
		result.ModelVersion = r.config.Model
	}
	if err := ValidateAmbiguityResult(request, result); err != nil {
		return AmbiguityResult{}, err
	}
	return result, nil
}
