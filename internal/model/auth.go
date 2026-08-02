package model

// Auth is a tagged union; Type selects which nested struct is populated.
type Auth struct {
	Type   AuthType    `json:"type"`
	Basic  *BasicAuth  `json:"basic,omitempty"`
	Bearer *BearerAuth `json:"bearer,omitempty"`
	APIKey *APIKeyAuth `json:"apiKey,omitempty"`
	OAuth2 *OAuth2Auth `json:"oauth2,omitempty"`
}

// BasicAuth carries HTTP Basic credentials.
type BasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// BearerAuth carries a bearer token.
type BearerAuth struct {
	Token string `json:"token"`
}

// APIKeyAuth adds an API key either as a header or a query parameter.
type APIKeyAuth struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	In    string `json:"in"` // "header" or "query"
}

// OAuth2Auth configures an OAuth 2.0 grant. Token acquisition and refresh are handled
// by the request engine; on disk, secret fields hold references to keychain-backed
// values rather than raw secrets.
type OAuth2Auth struct {
	GrantType    string `json:"grantType,omitempty"`
	AuthURL      string `json:"authUrl,omitempty"`
	TokenURL     string `json:"tokenUrl,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Scope        string `json:"scope,omitempty"`
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
}
