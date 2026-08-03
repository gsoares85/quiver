package model

// Auth is a tagged union; Type selects which nested struct is populated.
type Auth struct {
	Type   AuthType    `json:"type" yaml:"type"`
	Basic  *BasicAuth  `json:"basic,omitempty" yaml:"basic,omitempty"`
	Bearer *BearerAuth `json:"bearer,omitempty" yaml:"bearer,omitempty"`
	APIKey *APIKeyAuth `json:"apiKey,omitempty" yaml:"apiKey,omitempty"`
	OAuth2 *OAuth2Auth `json:"oauth2,omitempty" yaml:"oauth2,omitempty"`
}

// BasicAuth carries HTTP Basic credentials.
type BasicAuth struct {
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
}

// BearerAuth carries a bearer token.
type BearerAuth struct {
	Token string `json:"token" yaml:"token"`
}

// APIKeyAuth adds an API key either as a header or a query parameter.
type APIKeyAuth struct {
	Key   string `json:"key" yaml:"key"`
	Value string `json:"value" yaml:"value"`
	In    string `json:"in" yaml:"in"` // "header" or "query"
}

// OAuth2Auth configures an OAuth 2.0 grant. Token acquisition and refresh are handled
// by the request engine; on disk, secret fields hold references to keychain-backed
// values rather than raw secrets.
type OAuth2Auth struct {
	GrantType    string `json:"grantType,omitempty" yaml:"grantType,omitempty"`
	AuthURL      string `json:"authUrl,omitempty" yaml:"authUrl,omitempty"`
	TokenURL     string `json:"tokenUrl,omitempty" yaml:"tokenUrl,omitempty"`
	ClientID     string `json:"clientId,omitempty" yaml:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty" yaml:"clientSecret,omitempty"`
	Scope        string `json:"scope,omitempty" yaml:"scope,omitempty"`
	AccessToken  string `json:"accessToken,omitempty" yaml:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty" yaml:"refreshToken,omitempty"`
}
