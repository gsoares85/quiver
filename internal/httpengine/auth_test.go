package httpengine

import (
	"context"
	"net/http"
	"testing"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

// authRequest builds a bare wire request for auth tests.
func authRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	hr, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	return hr
}

func TestApplyAuth(t *testing.T) {
	tests := []struct {
		name       string
		auth       *model.Auth
		wantHeader map[string]string
		wantQuery  string
	}{
		{
			name:      "no auth configured",
			auth:      nil,
			wantQuery: "page=2",
		},
		{
			name:      "explicitly none",
			auth:      &model.Auth{Type: model.AuthNone},
			wantQuery: "page=2",
		},
		{
			name: "basic",
			auth: &model.Auth{Type: model.AuthBasic, Basic: &model.BasicAuth{
				Username: "ada", Password: "l0velace",
			}},
			wantHeader: map[string]string{"Authorization": "Basic YWRhOmwwdmVsYWNl"},
			wantQuery:  "page=2",
		},
		{
			// Sending a token as the username with an empty password is a common API
			// convention, so it must not be rejected.
			name: "basic with an empty password",
			auth: &model.Auth{Type: model.AuthBasic, Basic: &model.BasicAuth{
				Username: "tok_123",
			}},
			wantHeader: map[string]string{"Authorization": "Basic dG9rXzEyMzo="},
			wantQuery:  "page=2",
		},
		{
			name:       "bearer",
			auth:       &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "abc123"}},
			wantHeader: map[string]string{"Authorization": "Bearer abc123"},
			wantQuery:  "page=2",
		},
		{
			// A pasted token routinely arrives with a trailing newline, which the
			// transport would reject outright.
			name:       "bearer with pasted whitespace",
			auth:       &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "  abc123\n"}},
			wantHeader: map[string]string{"Authorization": "Bearer abc123"},
			wantQuery:  "page=2",
		},
		{
			name: "api key in a header",
			auth: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{
				Key: "X-Api-Key", Value: "k-1", In: "header",
			}},
			wantHeader: map[string]string{"X-Api-Key": "k-1"},
			wantQuery:  "page=2",
		},
		{
			name: "api key defaults to a header",
			auth: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{
				Key: "X-Api-Key", Value: "k-1",
			}},
			wantHeader: map[string]string{"X-Api-Key": "k-1"},
			wantQuery:  "page=2",
		},
		{
			name: "api key in the query",
			auth: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{
				Key: "api key", Value: "k/1", In: "  Query ",
			}},
			wantQuery: "page=2&api+key=k%2F1",
		},
		{
			name: "oauth2 sends the acquired access token",
			auth: &model.Auth{Type: model.AuthOAuth2, OAuth2: &model.OAuth2Auth{
				GrantType: "client_credentials", AccessToken: "at-1", RefreshToken: "rt-1",
			}},
			wantHeader: map[string]string{"Authorization": "Bearer at-1"},
			wantQuery:  "page=2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hr := authRequest(t, "https://api.example.com/v1/users?page=2")
			require.NoError(t, applyAuth(hr, tt.auth))

			for key, want := range tt.wantHeader {
				require.Equal(t, want, hr.Header.Get(key))
			}
			if tt.wantHeader == nil {
				require.Empty(t, hr.Header.Get("Authorization"))
			}
			require.Equal(t, tt.wantQuery, hr.URL.RawQuery)
		})
	}
}

func TestApplyBasicRoundTrips(t *testing.T) {
	hr := authRequest(t, "https://api.example.com/v1")
	require.NoError(t, applyAuth(hr, &model.Auth{Type: model.AuthBasic, Basic: &model.BasicAuth{
		Username: "ada", Password: "p@ss:word ",
	}}))

	// A password may legitimately contain a colon or trailing whitespace: the pair is
	// base64-encoded, so it must survive untouched.
	user, pass, ok := hr.BasicAuth()
	require.True(t, ok)
	require.Equal(t, "ada", user)
	require.Equal(t, "p@ss:word ", pass)
}

func TestApplyAuthErrors(t *testing.T) {
	tests := []struct {
		name    string
		auth    *model.Auth
		wantMsg string
	}{
		{
			name:    "inherited auth reaching the engine",
			auth:    &model.Auth{Type: model.AuthInherit},
			wantMsg: "inherited auth must be flattened by the caller before execution",
		},
		{
			name:    "unknown type",
			auth:    &model.Auth{Type: model.AuthType("hawk")},
			wantMsg: `unknown auth type "hawk"`,
		},
		{
			name:    "basic without credentials",
			auth:    &model.Auth{Type: model.AuthBasic},
			wantMsg: "basic auth has no credentials",
		},
		{
			name:    "bearer without credentials",
			auth:    &model.Auth{Type: model.AuthBearer},
			wantMsg: "bearer auth has no credentials",
		},
		{
			name:    "bearer with an empty token",
			auth:    &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "   "}},
			wantMsg: "bearer token is empty",
		},
		{
			name:    "bearer with an injected line break",
			auth:    &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "abc\r\nX-Evil: 1"}},
			wantMsg: "bearer value contains a line break",
		},
		{
			name:    "api key without credentials",
			auth:    &model.Auth{Type: model.AuthAPIKey},
			wantMsg: "api key auth has no credentials",
		},
		{
			name:    "api key without a name",
			auth:    &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{Value: "k-1"}},
			wantMsg: "api key auth has no key name",
		},
		{
			name: "api key with an injected line break",
			auth: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{
				Key: "X-Api-Key", Value: "k-1\nX-Evil: 1",
			}},
			wantMsg: "api key value contains a line break",
		},
		{
			name: "api key in an unknown place",
			auth: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{
				Key: "X-Api-Key", Value: "k-1", In: "cookie",
			}},
			wantMsg: `unknown api key location "cookie", want "header" or "query"`,
		},
		{
			name:    "oauth2 without credentials",
			auth:    &model.Auth{Type: model.AuthOAuth2},
			wantMsg: "oauth2 auth has no credentials",
		},
		{
			name: "oauth2 before a token was acquired",
			auth: &model.Auth{Type: model.AuthOAuth2, OAuth2: &model.OAuth2Auth{
				GrantType: "authorization_code", ClientID: "cid",
			}},
			wantMsg: "oauth2 access token is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hr := authRequest(t, "https://api.example.com/v1/users")
			err := applyAuth(hr, tt.auth)

			require.ErrorContains(t, err, tt.wantMsg)
			require.Equal(t, KindRequest, KindOf(err))

			var engineErr *Error
			require.ErrorAs(t, err, &engineErr)
			require.Equal(t, opApplyAuth, engineErr.Op)
			require.Equal(t, "https://api.example.com/v1/users", engineErr.URL)

			// A rejected request must carry no half-applied credentials.
			require.Empty(t, hr.Header.Get("Authorization"))
		})
	}
}

func TestApplyAuthNeverLeaksTheSecret(t *testing.T) {
	const secret = "sk-live-do-not-log"

	tests := []*model.Auth{
		{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: secret + "\n" + secret}},
		{Type: model.AuthOAuth2, OAuth2: &model.OAuth2Auth{AccessToken: secret + "\r\nX-Evil: 1"}},
		{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{Key: "X-Api-Key", Value: secret + "\nX-Evil: 1"}},
		{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{Key: "X-Api-Key", Value: secret, In: "cookie"}},
	}

	for _, auth := range tests {
		t.Run(string(auth.Type), func(t *testing.T) {
			err := applyAuth(authRequest(t, "https://api.example.com/v1"), auth)
			require.Error(t, err)
			require.NotContains(t, err.Error(), secret, "an error message must never carry a credential")
		})
	}
}

func TestNewRequestAppliesAuth(t *testing.T) {
	hr, err := newRequest(context.Background(), model.Request{
		Method: http.MethodGet,
		URL:    "https://api.example.com/v1/users",
		Auth:   &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "abc123"}},
	}, newFakeOpener(nil))
	require.NoError(t, err)
	require.Equal(t, "Bearer abc123", hr.Header.Get("Authorization"))
}

func TestNewRequestAuthOverridesATypedHeader(t *testing.T) {
	hr, err := newRequest(context.Background(), model.Request{
		Method:  http.MethodGet,
		URL:     "https://api.example.com/v1/users",
		Headers: []model.Header{{Key: "Authorization", Value: "Bearer stale-pasted-token"}},
		Auth:    &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "fresh"}},
	}, newFakeOpener(nil))
	require.NoError(t, err)

	require.Equal(t, []string{"Bearer fresh"}, hr.Header.Values("Authorization"),
		"configured auth replaces a hand-typed header rather than piling onto it")
}

func TestNewRequestAppliesAPIKeyAfterTheAuthoredQuery(t *testing.T) {
	hr, err := newRequest(context.Background(), model.Request{
		Method: http.MethodGet,
		URL:    "https://api.example.com/v1/users?page=2",
		Query:  []model.Param{{Key: "sort", Value: "name"}},
		Auth: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{
			Key: "api_key", Value: "k-1", In: "query",
		}},
	}, newFakeOpener(nil))
	require.NoError(t, err)

	require.Equal(t, "page=2&sort=name&api_key=k-1", hr.URL.RawQuery)
}

func TestNewRequestReportsAuthFailure(t *testing.T) {
	hr, err := newRequest(context.Background(), model.Request{
		Method: http.MethodGet,
		URL:    "https://api.example.com/v1/users",
		Auth:   &model.Auth{Type: model.AuthInherit},
	}, newFakeOpener(nil))

	require.Nil(t, hr)
	require.Equal(t, KindRequest, KindOf(err))
	require.ErrorContains(t, err, "flattened by the caller")
}
