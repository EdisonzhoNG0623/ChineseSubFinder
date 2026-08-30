package settings

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
)

// SearchPolicySchemaVersion is bumped only when query construction or
// deterministic identity semantics change. The value is deliberately part of
// the persisted task fingerprint so a real search upgrade can wake old misses.
const SearchPolicySchemaVersion = "3"

var currentSearchPolicyCache struct {
	sync.Mutex
	settings *Settings
	revision uint64
	value    string
}

type supplierPolicyMaterial struct {
	Name       string `json:"name"`
	RootURL    string `json:"root_url"`
	DailyLimit int    `json:"daily_limit"`
}

type searchPolicyMaterial struct {
	Revision      uint64                   `json:"revision"`
	SchemaVersion string                   `json:"schema_version"`
	Topic         int                      `json:"topic"`
	SubType       int                      `json:"sub_type"`
	SaveMulti     bool                     `json:"save_multi"`
	Suppliers     []supplierPolicyMaterial `json:"suppliers"`
	Assrt         bool                     `json:"assrt"`
	SubtitleBest  bool                     `json:"subtitle_best"`
	SubDL         bool                     `json:"subdl"`
	OpenSubtitles bool                     `json:"open_subtitles"`
	OpenUseHash   bool                     `json:"open_use_hash"`
	OpenAI        bool                     `json:"open_ai"`
	OpenMachine   bool                     `json:"open_machine"`
	SubSource     bool                     `json:"subsource"`
	AnimeTosho    bool                     `json:"animetosho"`
	Addic7ed      bool                     `json:"addic7ed"`
	ProxyEnabled  bool                     `json:"proxy_enabled"`
	ProxyProtocol string                   `json:"proxy_protocol"`
	ProxyAddress  string                   `json:"proxy_address"`
	TMDBEnabled   bool                     `json:"tmdb_enabled"`
	TMDBAlternate bool                     `json:"tmdb_alternate"`
	AIEnabled     bool                     `json:"ai_enabled"`
	AIEndpoint    string                   `json:"ai_endpoint"`
	AIModel       string                   `json:"ai_model"`
	AIConfidence  float64                  `json:"ai_confidence"`
}

// SearchPolicyFingerprint is safe to persist and expose in diagnostics. It
// contains no credential material, media path, title or endpoint query data.
// Credential replacement is represented by the server-owned revision.
func SearchPolicyFingerprint(value *Settings) string {
	material := publicSearchPolicyMaterial(value)
	encoded, err := json.Marshal(material)
	if err != nil {
		return SearchPolicySchemaVersion
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%s-%x", SearchPolicySchemaVersion, sum[:12])
}

// CurrentSearchPolicyFingerprint returns the active fingerprint without
// re-marshalling settings for every queue row rendered by diagnostics. Normal
// settings updates replace the Settings pointer and increment the revision,
// which invalidates this small process-local cache.
func CurrentSearchPolicyFingerprint() string {
	value := Get()
	currentSearchPolicyCache.Lock()
	defer currentSearchPolicyCache.Unlock()
	if currentSearchPolicyCache.settings == value && currentSearchPolicyCache.revision == value.SearchPolicyRevision &&
		currentSearchPolicyCache.value != "" {
		return currentSearchPolicyCache.value
	}
	fingerprint := SearchPolicyFingerprint(value)
	currentSearchPolicyCache.settings = value
	currentSearchPolicyCache.revision = value.SearchPolicyRevision
	currentSearchPolicyCache.value = fingerprint
	return fingerprint
}

func publicSearchPolicyMaterial(value *Settings) searchPolicyMaterial {
	material := searchPolicyMaterial{SchemaVersion: SearchPolicySchemaVersion}
	if value == nil {
		return material
	}
	material.Revision = value.SearchPolicyRevision
	if advanced := value.AdvancedSettings; advanced != nil {
		material.Topic = advanced.Topic
		material.SubType = advanced.SubTypePriority
		material.SaveMulti = advanced.SaveMultiSub
		if proxy := advanced.ProxySettings; proxy != nil {
			material.ProxyEnabled = proxy.UseProxy
			material.ProxyProtocol = strings.TrimSpace(proxy.UseWhichProxyProtocol)
			material.ProxyAddress = publicEndpointIdentity(proxy.LocalHttpProxyServerPort) + "\x00" +
				publicEndpointIdentity(proxy.InputProxyAddress) + "\x00" + strings.TrimSpace(proxy.InputProxyPort)
		}
		material.TMDBEnabled = advanced.TmdbApiSettings.Enable
		material.TMDBAlternate = advanced.TmdbApiSettings.UseAlternateBaseURL
		if suppliers := advanced.SuppliersSettings; suppliers != nil {
			for _, supplier := range []*OneSupplierSettings{suppliers.Xunlei, suppliers.Shooter, suppliers.Assrt,
				suppliers.A4k, suppliers.SubHD, suppliers.Zimuku, suppliers.SubtitleBest, suppliers.SubDL,
				suppliers.AnimeTosho, suppliers.Addic7ed} {
				if supplier == nil {
					continue
				}
				material.Suppliers = append(material.Suppliers, supplierPolicyMaterial{
					Name: strings.TrimSpace(supplier.Name), RootURL: publicEndpointIdentity(supplier.RootUrl),
					DailyLimit: supplier.DailyDownloadLimit,
				})
			}
		}
	}
	if sources := value.SubtitleSources; sources != nil {
		material.Assrt = sources.AssrtSettings.Enabled && strings.TrimSpace(sources.AssrtSettings.Token) != ""
		material.SubtitleBest = sources.SubtitleBestSettings.Enabled && strings.TrimSpace(sources.SubtitleBestSettings.ApiKey) != ""
		material.SubDL = sources.SubDLSettings.Enabled && strings.TrimSpace(sources.SubDLSettings.ApiKey) != ""
		open := sources.OpenSubtitlesSettings
		material.OpenSubtitles = open.Enabled && strings.TrimSpace(open.APIKey) != "" &&
			strings.TrimSpace(open.Username) != "" && open.Password != ""
		material.OpenUseHash, material.OpenAI, material.OpenMachine = open.UseHash, open.IncludeAITranslated, open.IncludeMachineTranslated
		material.SubSource = sources.SubSourceSettings.Enabled && strings.TrimSpace(sources.SubSourceSettings.APIKey) != ""
		material.AnimeTosho = sources.AnimeToshoSettings.Enabled
		material.Addic7ed = sources.Addic7edSettings.Enabled
	}
	if value.ExperimentalFunction != nil {
		ai := value.ExperimentalFunction.AISettings
		material.AIEnabled = ai.Enabled
		material.AIEndpoint = publicEndpointIdentity(ai.BaseURL)
		material.AIModel = strings.TrimSpace(ai.Model)
		material.AIConfidence = ai.MinConfidence
	}
	return material
}

// publicEndpointIdentity keeps routing-relevant endpoint structure while
// excluding userinfo, query values and fragments. Those private/raw values are
// compared only in memory and represented externally by SearchPolicyRevision.
func publicEndpointIdentity(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	candidate := raw
	schemeRelative := !strings.Contains(candidate, "://") && !strings.HasPrefix(candidate, "/")
	if schemeRelative {
		candidate = "//" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return "invalid-endpoint"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	result := parsed.String()
	if schemeRelative {
		result = strings.TrimPrefix(result, "//")
	}
	return result
}

// searchPolicyChanged compares secrets only in memory and never serializes or
// logs them. A credential rotation increments the persisted revision without
// placing a credential-derived hash in queue files or API responses.
func searchPolicyChanged(current, incoming *Settings) bool {
	currentMaterial := publicSearchPolicyMaterial(current)
	incomingMaterial := publicSearchPolicyMaterial(incoming)
	// Revision is the result of this comparison, never an input controlled by
	// the settings client.
	currentMaterial.Revision = 0
	incomingMaterial.Revision = 0
	if !reflect.DeepEqual(currentMaterial, incomingMaterial) {
		return true
	}
	return !reflect.DeepEqual(privateSearchCredentials(current), privateSearchCredentials(incoming))
}

func privateSearchCredentials(value *Settings) []string {
	if value == nil {
		return nil
	}
	credentials := make([]string, 0, 12)
	if value.SubtitleSources != nil {
		sources := value.SubtitleSources
		credentials = append(credentials, sources.AssrtSettings.Token, sources.SubtitleBestSettings.ApiKey,
			sources.SubDLSettings.ApiKey, sources.OpenSubtitlesSettings.APIKey, sources.OpenSubtitlesSettings.Username,
			sources.OpenSubtitlesSettings.Password, sources.SubSourceSettings.APIKey)
	}
	if value.AdvancedSettings != nil {
		credentials = append(credentials, value.AdvancedSettings.TmdbApiSettings.ApiKey)
		if value.AdvancedSettings.ProxySettings != nil {
			credentials = append(credentials, value.AdvancedSettings.ProxySettings.InputProxyUsername,
				value.AdvancedSettings.ProxySettings.InputProxyPassword,
				value.AdvancedSettings.ProxySettings.LocalHttpProxyServerPort,
				value.AdvancedSettings.ProxySettings.InputProxyAddress)
		}
		if suppliers := value.AdvancedSettings.SuppliersSettings; suppliers != nil {
			for _, supplier := range []*OneSupplierSettings{suppliers.Xunlei, suppliers.Shooter, suppliers.Assrt,
				suppliers.A4k, suppliers.SubHD, suppliers.Zimuku, suppliers.SubtitleBest, suppliers.SubDL,
				suppliers.AnimeTosho, suppliers.Addic7ed} {
				if supplier != nil {
					credentials = append(credentials, supplier.RootUrl)
				}
			}
		}
	}
	if value.ExperimentalFunction != nil {
		credentials = append(credentials, value.ExperimentalFunction.AISettings.APIKey,
			value.ExperimentalFunction.AISettings.BaseURL)
	}
	return credentials
}
