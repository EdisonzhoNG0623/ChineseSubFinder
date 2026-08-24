package ai_ambiguity

import (
	"context"
	"sync"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/episode_identity"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
)

type RuntimeStatus struct {
	Enabled       bool      `json:"enabled"`
	Configured    bool      `json:"configured"`
	Attempts      int64     `json:"attempts"`
	Matches       int64     `json:"matches"`
	NoMatches     int64     `json:"no_matches"`
	Abstentions   int64     `json:"abstentions"`
	Errors        int64     `json:"errors"`
	LastLatencyMs int64     `json:"last_latency_millis"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
}

var runtimeState struct {
	sync.RWMutex
	status RuntimeStatus
}

type measuredResolver struct {
	backend episode_identity.AmbiguityResolver
}

func (m measuredResolver) ResolveAmbiguity(ctx context.Context, request episode_identity.AmbiguityRequest) (episode_identity.AmbiguityResult, error) {
	startedAt := time.Now()
	result, err := m.backend.ResolveAmbiguity(ctx, request)
	runtimeState.Lock()
	runtimeState.status.Attempts++
	runtimeState.status.LastAttemptAt = time.Now()
	runtimeState.status.LastLatencyMs = time.Since(startedAt).Milliseconds()
	if err != nil {
		runtimeState.status.Errors++
	} else {
		switch result.Decision {
		case episode_identity.AmbiguityMatch:
			runtimeState.status.Matches++
		case episode_identity.AmbiguityNoMatch:
			runtimeState.status.NoMatches++
		default:
			runtimeState.status.Abstentions++
		}
	}
	runtimeState.Unlock()
	return result, err
}

func ConfiguredResolver() episode_identity.AmbiguityResolver {
	s, ok := settings.GetIfInitialized()
	if !ok || s.ExperimentalFunction == nil {
		setConfigurationStatus(false, false)
		return episode_identity.DisabledAmbiguityResolver{}
	}
	config := s.ExperimentalFunction.AISettings
	configured := config.BaseURL != "" && config.Model != ""
	setConfigurationStatus(config.Enabled, configured)
	if !config.Enabled || !configured || config.Validate() != nil {
		return episode_identity.DisabledAmbiguityResolver{}
	}
	backend, err := episode_identity.NewOpenAICompatibleResolver(episode_identity.OpenAICompatibleConfig{
		BaseURL: config.BaseURL, APIKey: config.APIKey, Model: config.Model,
		Timeout: time.Duration(config.TimeoutSeconds) * time.Second, AllowInsecure: config.AllowInsecureHTTP,
	})
	if err != nil {
		return episode_identity.DisabledAmbiguityResolver{}
	}
	return episode_identity.GuardedAmbiguityResolver{Enabled: true, Backend: measuredResolver{backend: backend}, MinConfidence: config.MinConfidence}
}

func setConfigurationStatus(enabled, configured bool) {
	runtimeState.Lock()
	runtimeState.status.Enabled, runtimeState.status.Configured = enabled, configured
	runtimeState.Unlock()
}

func Status() RuntimeStatus {
	runtimeState.RLock()
	status := runtimeState.status
	runtimeState.RUnlock()
	if s, ok := settings.GetIfInitialized(); ok && s.ExperimentalFunction != nil {
		config := s.ExperimentalFunction.AISettings
		status.Enabled = config.Enabled
		status.Configured = config.BaseURL != "" && config.Model != ""
	}
	return status
}
