package settings

// PublicSourceSettings controls a subtitle source that does not require user
// credentials. It is kept separate from the advanced endpoint settings so an
// existing installation never starts making new network requests implicitly.
type PublicSourceSettings struct {
	Enabled bool `json:"enabled"`
}
