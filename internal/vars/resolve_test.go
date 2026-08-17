package vars

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

// fullRequest is a request that uses a variable in every place one can appear. It is built by
// a function rather than shared, so a test can hold a pristine second copy and prove the
// original was not touched.
func fullRequest() model.Request {
	return model.Request{
		ID:     "req-1",
		Name:   "create user",
		Method: "POST",
		URL:    "{{baseUrl}}/{{version}}/users",
		Query: []model.Header{
			{Key: "{{filterKey}}", Value: "{{filterValue}}", Enabled: true},
			{Key: "static", Value: "kept", Enabled: true},
		},
		Headers: []model.Header{
			{Key: "X-{{tenant}}-Id", Value: "{{tenantId}}", Enabled: true},
			{Key: "Accept", Value: "application/json", Enabled: true},
		},
		Auth: &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "{{token}}"}},
		Body: &model.Body{
			Type: model.BodyJSON,
			Text: `{"name":"{{userName}}","tenant":"{{tenant}}"}`,
		},
		PreRequest: `qv.variables.set("ignored", "{{notAVariableHere}}")`,
		Test:       `qv.expect(qv.response.status).toBe({{expectedStatus}})`,
		Settings:   model.RequestSettings{FollowRedirects: true, MaxRedirects: 5},
	}
}

// fullChain defines everything fullRequest refers to, except what a test deliberately leaves out.
func fullChain() *Chain {
	return NewChain(Scope{
		v("baseUrl", "https://api.example.com"),
		v("version", "v1"),
		v("filterKey", "status"),
		v("filterValue", "active"),
		v("tenant", "acme"),
		v("tenantId", "t-42"),
		v("token", "tok-abc"),
		v("userName", "ada"),
	})
}

func TestResolveSubstitutesEveryField(t *testing.T) {
	got, err := Resolve(context.Background(), fullRequest(), fullChain(), nil)
	require.NoError(t, err)

	require.Equal(t, "https://api.example.com/v1/users", got.Request.URL)
	require.Equal(t, []model.Header{
		{Key: "status", Value: "active", Enabled: true},
		{Key: "static", Value: "kept", Enabled: true},
	}, got.Request.Query, "keys and values both resolve")
	require.Equal(t, []model.Header{
		{Key: "X-acme-Id", Value: "t-42", Enabled: true},
		{Key: "Accept", Value: "application/json", Enabled: true},
	}, got.Request.Headers)
	require.Equal(t, "tok-abc", got.Request.Auth.Bearer.Token)
	require.Equal(t, `{"name":"ada","tenant":"acme"}`, got.Request.Body.Text)

	// Everything that is not a variable comes through untouched.
	require.Equal(t, "req-1", got.Request.ID)
	require.Equal(t, "POST", got.Request.Method)
	require.Equal(t, model.RequestSettings{FollowRedirects: true, MaxRedirects: 5}, got.Request.Settings)
}

func TestResolveLeavesScriptsAlone(t *testing.T) {
	// Scripts read variables through the scripting API. Substituting into them would splice
	// text into code, and a reference inside one is not a reference this package owns.
	req := fullRequest()
	got, err := Resolve(context.Background(), req, fullChain(), nil)
	require.NoError(t, err)

	require.Equal(t, req.PreRequest, got.Request.PreRequest)
	require.Equal(t, req.Test, got.Request.Test,
		"a {{name}} in a script is left for the script host, not reported as unresolved")
}

func TestResolveLeavesTheOriginalUntouched(t *testing.T) {
	// A caller holds the stored entity; resolution is a copy for one send. Comparing against
	// a second, pristine build of the same fixture catches any write-through.
	original := fullRequest()
	pristine := fullRequest()

	_, err := Resolve(context.Background(), original, fullChain(), nil)
	require.NoError(t, err)
	require.Equal(t, pristine, original)
}

func TestResolveBodyShapes(t *testing.T) {
	chain := NewChain(Scope{
		v("text", "hello"),
		v("field", "avatar"),
		v("dir", "/fixtures"),
		v("id", "42"),
		v("op", "GetUser"),
	})

	tests := []struct {
		name string
		body *model.Body
		want *model.Body
	}{
		{name: "no body", body: nil, want: nil},
		{
			name: "text",
			body: &model.Body{Type: model.BodyText, Text: "{{text}} there"},
			want: &model.Body{Type: model.BodyText, Text: "hello there"},
		},
		{
			name: "form drops what is switched off",
			body: &model.Body{Type: model.BodyForm, Form: []model.Header{
				{Key: "{{field}}", Value: "{{text}}", Enabled: true},
				{Key: "unused", Value: "gone", Enabled: false},
			}},
			want: &model.Body{Type: model.BodyForm, Form: []model.Header{
				{Key: "avatar", Value: "hello", Enabled: true},
			}},
		},
		{
			name: "multipart resolves field names and paths",
			body: &model.Body{Type: model.BodyMultipart, Files: []model.FileRef{
				{Field: "{{field}}", Path: "{{dir}}/logo.png"},
			}},
			want: &model.Body{Type: model.BodyMultipart, Files: []model.FileRef{
				{Field: "avatar", Path: "/fixtures/logo.png"},
			}},
		},
		{
			name: "graphql resolves the query, its variables, and the operation",
			body: &model.Body{
				Type:    model.BodyGraphQL,
				Text:    "query {{op}}($id: ID!) { user(id: $id) { name } }",
				GraphQL: &model.GraphQL{Variables: `{"id":"{{id}}"}`, OperationName: "{{op}}"},
			},
			want: &model.Body{
				Type:    model.BodyGraphQL,
				Text:    "query GetUser($id: ID!) { user(id: $id) { name } }",
				GraphQL: &model.GraphQL{Variables: `{"id":"42"}`, OperationName: "GetUser"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(context.Background(), model.Request{
				Method: "POST", URL: "https://x/v1", Body: tt.body,
			}, chain, nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, got.Request.Body)
		})
	}
}

func TestResolveAuthVariants(t *testing.T) {
	chain := NewChain(Scope{
		v("user", "ada"),
		v("pass", "l0velace"),
		v("token", "tok-abc"),
		v("keyName", "X-Api-Key"),
		v("keyValue", "k-1"),
		v("clientId", "cid"),
		v("clientSecret", "csecret"),
		v("tokenUrl", "https://auth.example.com/token"),
	})

	tests := []struct {
		name string
		auth *model.Auth
		want *model.Auth
	}{
		{name: "none configured", auth: nil, want: nil},
		{
			name: "basic",
			auth: &model.Auth{Type: model.AuthBasic, Basic: &model.BasicAuth{Username: "{{user}}", Password: "{{pass}}"}},
			want: &model.Auth{Type: model.AuthBasic, Basic: &model.BasicAuth{Username: "ada", Password: "l0velace"}},
		},
		{
			name: "bearer",
			auth: &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "{{token}}"}},
			want: &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "tok-abc"}},
		},
		{
			name: "api key keeps its placement literal",
			auth: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{
				Key: "{{keyName}}", Value: "{{keyValue}}", In: "header",
			}},
			want: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{
				Key: "X-Api-Key", Value: "k-1", In: "header",
			}},
		},
		{
			name: "oauth2 keeps its grant type literal",
			auth: &model.Auth{Type: model.AuthOAuth2, OAuth2: &model.OAuth2Auth{
				GrantType:    "client_credentials",
				TokenURL:     "{{tokenUrl}}",
				ClientID:     "{{clientId}}",
				ClientSecret: "{{clientSecret}}",
			}},
			want: &model.Auth{Type: model.AuthOAuth2, OAuth2: &model.OAuth2Auth{
				GrantType:    "client_credentials",
				TokenURL:     "https://auth.example.com/token",
				ClientID:     "cid",
				ClientSecret: "csecret",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(context.Background(), model.Request{
				Method: "GET", URL: "https://x/v1", Auth: tt.auth,
			}, chain, nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, got.Request.Auth)
		})
	}
}

func TestResolveDropsDisabledEntries(t *testing.T) {
	// The engine trusts what it is handed and does not filter again, so this is the only place
	// a switched-off entry can be removed.
	got, err := Resolve(context.Background(), model.Request{
		Method: "GET",
		URL:    "https://x/v1",
		Query: []model.Header{
			{Key: "keep", Value: "1", Enabled: true},
			{Key: "drop", Value: "2", Enabled: false},
		},
		Headers: []model.Header{
			{Key: "X-Keep", Value: "1", Enabled: true},
			{Key: "X-Drop", Value: "{{neverEvaluated}}", Enabled: false},
		},
	}, NewChain(), nil)
	require.NoError(t, err)

	require.Equal(t, []model.Header{{Key: "keep", Value: "1", Enabled: true}}, got.Request.Query)
	require.Equal(t, []model.Header{{Key: "X-Keep", Value: "1", Enabled: true}}, got.Request.Headers,
		"a disabled entry is dropped before its references are even looked at")
}

func TestResolveKeepsAbsentFieldsAbsent(t *testing.T) {
	got, err := Resolve(context.Background(), model.Request{
		Method: "GET", URL: "https://api.example.com/health",
	}, NewChain(), nil)
	require.NoError(t, err)

	require.Nil(t, got.Request.Headers)
	require.Nil(t, got.Request.Query)
	require.Nil(t, got.Request.Body)
	require.Nil(t, got.Request.Auth)
	require.Empty(t, got.Secrets.Values())
}

func TestResolveReportsEveryUnresolvedName(t *testing.T) {
	// Three missing variables should cost one round trip, not three.
	_, err := Resolve(context.Background(), model.Request{
		Method:  "POST",
		URL:     "{{missingHost}}/v1",
		Headers: []model.Header{{Key: "X-Trace", Value: "{{missingTrace}}", Enabled: true}},
		Body:    &model.Body{Type: model.BodyJSON, Text: `{"id":"{{missingId}}"}`},
	}, NewChain(Scope{v("known", "yes")}), nil)

	var unresolved *UnresolvedError
	require.ErrorAs(t, err, &unresolved)
	require.Equal(t, []string{"missingHost", "missingTrace", "missingId"}, unresolved.Names,
		"reported in the order a request is read")
	require.EqualError(t, err, "unresolved variables: {{missingHost}}, {{missingTrace}}, {{missingId}}")
}

func TestResolveUnresolvedErrorWordsOneNameSingly(t *testing.T) {
	_, err := Resolve(context.Background(), model.Request{
		Method: "GET", URL: "{{missingHost}}/v1",
	}, NewChain(), nil)

	require.EqualError(t, err, "unresolved variable: {{missingHost}}")
}

func TestResolveReportsCycles(t *testing.T) {
	_, err := Resolve(context.Background(), model.Request{
		Method: "GET", URL: "{{a}}",
	}, NewChain(Scope{v("a", "{{b}}"), v("b", "{{a}}")}), nil)

	var cycle *CycleError
	require.ErrorAs(t, err, &cycle)
	require.Equal(t, []string{"a", "b", "a"}, cycle.Path)
}

func TestResolveSurfacesAHardFailureFromEveryField(t *testing.T) {
	// Every field is walked by the same helper, and every one of those walks can fail. A field
	// that swallowed a cycle would send a request built from a reference that never resolved,
	// so each is provoked in turn.
	looping := NewChain(Scope{v("a", "{{b}}"), v("b", "{{a}}")})

	tests := []struct {
		name string
		req  model.Request
	}{
		{name: "url", req: model.Request{URL: "{{a}}"}},
		{name: "query key", req: model.Request{Query: []model.Header{{Key: "{{a}}", Enabled: true}}}},
		{name: "query value", req: model.Request{Query: []model.Header{{Key: "k", Value: "{{a}}", Enabled: true}}}},
		{name: "header key", req: model.Request{Headers: []model.Header{{Key: "{{a}}", Enabled: true}}}},
		{name: "header value", req: model.Request{Headers: []model.Header{{Key: "k", Value: "{{a}}", Enabled: true}}}},
		{
			name: "basic auth",
			req:  model.Request{Auth: &model.Auth{Type: model.AuthBasic, Basic: &model.BasicAuth{Username: "{{a}}"}}},
		},
		{
			name: "bearer auth",
			req:  model.Request{Auth: &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "{{a}}"}}},
		},
		{
			name: "api key auth",
			req:  model.Request{Auth: &model.Auth{Type: model.AuthAPIKey, APIKey: &model.APIKeyAuth{Value: "{{a}}"}}},
		},
		{
			name: "oauth2 auth",
			req:  model.Request{Auth: &model.Auth{Type: model.AuthOAuth2, OAuth2: &model.OAuth2Auth{AccessToken: "{{a}}"}}},
		},
		{name: "body text", req: model.Request{Body: &model.Body{Type: model.BodyText, Text: "{{a}}"}}},
		{
			name: "body form",
			req:  model.Request{Body: &model.Body{Type: model.BodyForm, Form: []model.Header{{Key: "k", Value: "{{a}}", Enabled: true}}}},
		},
		{
			name: "upload path",
			req:  model.Request{Body: &model.Body{Type: model.BodyBinary, Files: []model.FileRef{{Path: "{{a}}"}}}},
		},
		{
			name: "graphql variables",
			req: model.Request{Body: &model.Body{
				Type: model.BodyGraphQL, GraphQL: &model.GraphQL{Variables: "{{a}}"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.req
			req.Method = "POST"
			if req.URL == "" {
				req.URL = "https://x/v1"
			}

			got, err := Resolve(context.Background(), req, looping, nil)
			require.Zero(t, got, "a request that could not be resolved is not half-returned")

			var cycle *CycleError
			require.ErrorAs(t, err, &cycle)
		})
	}
}

func TestResolveSubstitutesAndRecordsSecrets(t *testing.T) {
	const token = "sk-live-abc123"
	source := &fakeSource{values: map[string]string{"apiToken": token}}

	got, err := Resolve(context.Background(), model.Request{
		Method: "GET",
		URL:    "https://api.example.com/v1/me",
		Auth:   &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "{{apiToken}}"}},
	}, NewChain(Scope{secretVar("apiToken")}), source)
	require.NoError(t, err)

	require.Equal(t, token, got.Request.Auth.Bearer.Token)
	require.Equal(t, []string{token}, got.Secrets.Values())
	require.Equal(t, "Bearer "+Mask, got.Secrets.Redact("Bearer "+token),
		"the result carries what it needs to keep the credential out of a log")
}

func TestResolveRecordsASecretSetDuringTheRun(t *testing.T) {
	// The overlay is where a pre-request script will put the token it just exchanged
	// credentials for. It has to be masked like any other secret, even though no source was
	// ever involved.
	const token = "sk-live-abc123"
	chain := NewChain()
	chain.SetSecret("apiToken", token)

	got, err := Resolve(context.Background(), model.Request{
		Method: "GET", URL: "https://api.example.com/v1/me",
		Headers: []model.Header{{Key: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}},
	}, chain, nil)
	require.NoError(t, err)

	require.Equal(t, "Bearer "+token, got.Request.Headers[0].Value)
	require.Equal(t, []string{token}, got.Secrets.Values())
}

func TestResolveSurfacesASecretSourceFailure(t *testing.T) {
	locked := errors.New("keychain is locked")
	source := &fakeSource{err: locked}

	_, err := Resolve(context.Background(), model.Request{
		Method: "GET", URL: "https://x/v1",
		Headers: []model.Header{{Key: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}},
	}, NewChain(Scope{secretVar("apiToken")}), source)

	require.ErrorIs(t, err, locked)

	var unresolved *UnresolvedError
	require.NotErrorAs(t, err, &unresolved, "a source that failed is not a variable nobody defined")
}

func TestResolveWithoutAChain(t *testing.T) {
	// A nil chain simply defines nothing: a request with no references still resolves.
	got, err := Resolve(context.Background(), model.Request{
		Method: "GET", URL: "https://api.example.com/health",
	}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "https://api.example.com/health", got.Request.URL)

	_, err = Resolve(context.Background(), model.Request{
		Method: "GET", URL: "{{baseUrl}}/health",
	}, nil, nil)
	require.ErrorContains(t, err, "unresolved variable: {{baseUrl}}")
}

func TestResolveThroughAWorkspaceChain(t *testing.T) {
	// End to end with the real chain builder: the environment's value wins over the folders,
	// the collection, and the workspace, exactly as the cascade says it should.
	ws := nestedWorkspace()
	env, err := EnvironmentByName(ws, "staging")
	require.NoError(t, err)

	chain, err := ChainFor(ws, "req-deep", env, map[string]string{"path": "users"})
	require.NoError(t, err)

	got, err := Resolve(context.Background(), model.Request{
		Method: "GET",
		URL:    "https://api.example.com/{{path}}?scope={{target}}",
	}, chain, nil)
	require.NoError(t, err)
	require.Equal(t, "https://api.example.com/users?scope=environment", got.Request.URL)
}
