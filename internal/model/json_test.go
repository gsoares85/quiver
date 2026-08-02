package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceJSONRoundTrip(t *testing.T) {
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
			Items: []Item{{Folder: &Folder{
				ID:   "01J9Z3Q6FLDUSERS000000000",
				Name: "Users",
				Items: []Item{{Request: &Request{
					ID:       "01J9Z3P4REQGETUSER0000000",
					Name:     "Get user",
					Method:   "GET",
					URL:      "{{baseUrl}}/users/{{userId}}",
					Headers:  []Header{{Key: "Accept", Value: "application/json", Enabled: true}},
					Body:     &Body{Type: BodyJSON, Text: `{"a":1}`},
					Auth:     &Auth{Type: AuthInherit},
					Settings: RequestSettings{FollowRedirects: true, MaxRedirects: 10},
				}}},
			}}},
		}},
	}

	data, err := json.Marshal(ws)
	require.NoError(t, err)

	var got Workspace
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, ws, got)
}

func TestResponseJSONRoundTrip(t *testing.T) {
	r := Response{
		Status:     200,
		StatusText: "OK",
		Headers:    []Header{{Key: "Content-Type", Value: "application/json", Enabled: true}},
		Body:       []byte(`{"ok":true}`),
		Size:       11,
		Duration:   Duration(150 * time.Millisecond),
		Timing:     Timing{DNS: Duration(time.Millisecond), TTFB: Duration(100 * time.Millisecond)},
		Assertions: []TestResult{{Name: "status is 200", Passed: true}},
	}

	data, err := json.Marshal(r)
	require.NoError(t, err)

	var got Response
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, r, got)
}
