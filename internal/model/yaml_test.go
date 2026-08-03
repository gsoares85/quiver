package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestYAMLUsesCamelCaseKeys(t *testing.T) {
	out, err := yaml.Marshal(Workspace{
		SchemaVersion: 1,
		ID:            "id",
		Name:          "n",
		Settings:      Settings{FollowRedirects: true, TimeoutMs: 30000},
	})
	require.NoError(t, err)

	s := string(out)
	for _, key := range []string{"schemaVersion:", "followRedirects:", "timeoutMs:"} {
		require.Containsf(t, s, key, "yaml output should use the camelCase key %q", key)
	}
	// The default (untagged) behaviour lowercases field names; ensure the tags win.
	require.NotContains(t, s, "schemaversion:")
}

func TestYAMLRoundTrip(t *testing.T) {
	ws := Workspace{
		SchemaVersion: 1,
		ID:            "01J9Z3K7QV0000000000000000",
		Name:          "My API",
		Settings:      Settings{FollowRedirects: true, TimeoutMs: 30000},
		Variables:     []Variable{{Key: "apiVersion", Value: "v1", Enabled: true}},
		Environments: []Environment{{
			ID:        "01J9Z3M2ENVSTAGING00000000",
			Name:      "staging",
			Variables: []Variable{{Key: "apiToken", Secret: true, Enabled: true}},
		}},
		Collections: []Collection{{
			ID:   "01J9Z3R8COLUSERSAPI000000",
			Name: "Users API",
			Auth: &Auth{Type: AuthBearer, Bearer: &BearerAuth{Token: "{{apiToken}}"}},
			Items: []Item{{Request: &Request{
				ID:       "01J9Z3P4REQGETUSER0000000",
				Name:     "Get user",
				Method:   "GET",
				URL:      "{{baseUrl}}/users/{{userId}}",
				Headers:  []Header{{Key: "Accept", Value: "application/json", Enabled: true}},
				Body:     &Body{Type: BodyJSON, Text: `{"a":1}`},
				Settings: RequestSettings{FollowRedirects: true, MaxRedirects: 10},
			}}},
		}},
	}

	out, err := yaml.Marshal(ws)
	require.NoError(t, err)

	var got Workspace
	require.NoError(t, yaml.Unmarshal(out, &got))
	require.Equal(t, ws, got)
}
