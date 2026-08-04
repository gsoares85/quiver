package store

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update golden files in testdata/")

// golden compares got against testdata/<name>, or rewrites it when -update is set.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoErrorf(t, err, "missing golden %s (run: go test ./internal/store -run %s -update)", path, t.Name())
	require.Equal(t, string(want), string(got))
}

func sampleRequest() model.Request {
	return model.Request{
		ID:         "01J9Z3P4REQGETUSER0000000",
		Name:       "Get user",
		Method:     "GET",
		URL:        "{{baseUrl}}/users/{{userId}}",
		Headers:    []model.Header{{Key: "Accept", Value: "application/json", Enabled: true}},
		Query:      []model.Param{{Key: "include", Value: "profile", Enabled: false}},
		Auth:       &model.Auth{Type: model.AuthInherit},
		Body:       &model.Body{Type: model.BodyJSON, Text: "{\n  \"a\": 1\n}\n"},
		PreRequest: "qv.variables.set(\"userId\", \"42\");\n",
		Test:       "test(\"status is 200\", () => {\n  qv.expect(qv.response.status).toBe(200);\n});\n",
		Settings:   model.RequestSettings{FollowRedirects: true, MaxRedirects: 10},
	}
}

func TestGoldenFiles(t *testing.T) {
	manifest := fileManifest{
		SchemaVersion: currentSchemaVersion,
		ID:            "01J9Z3K7QV0000000000000000",
		Name:          "My API",
		Settings:      model.Settings{FollowRedirects: true, TimeoutMs: 30000},
		Variables:     []model.Variable{{Key: "apiVersion", Value: "v1", Enabled: true}},
		Order:         []string{"users-api"},
	}
	env := newFileEnvironment(model.Environment{
		ID:   "01J9Z3M2ENVSTAGING00000000",
		Name: "staging",
		Variables: []model.Variable{
			{Key: "baseUrl", Value: "https://staging.example.com", Enabled: true},
			{Key: "apiToken", Secret: true, Enabled: true},
		},
	})
	collection := fileCollection{
		SchemaVersion: currentSchemaVersion,
		ID:            "01J9Z3R8COLUSERSAPI000000",
		Name:          "Users API",
		Auth:          &model.Auth{Type: model.AuthBearer, Bearer: &model.BearerAuth{Token: "{{apiToken}}"}},
		Order:         []string{"auth", "users"},
	}
	folder := fileFolder{
		SchemaVersion: currentSchemaVersion,
		ID:            "01J9Z3Q6FLDUSERS000000000",
		Name:          "Users",
		Order:         []string{"list-users.qv.yaml", "get-user.qv.yaml"},
	}

	cases := []struct {
		name string
		v    any
	}{
		{"quiver.yaml", manifest},
		{"staging.qv.yaml", env},
		{"collection.qv.yaml", collection},
		{"folder.qv.yaml", folder},
		{"get-user.qv.yaml", newFileRequest(sampleRequest())},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := marshalYAML(c.v)
			require.NoError(t, err)
			golden(t, c.name, out)
		})
	}
}

func TestFileRequestIdempotent(t *testing.T) {
	first, err := marshalYAML(newFileRequest(sampleRequest()))
	require.NoError(t, err)

	var fr fileRequest
	require.NoError(t, unmarshalYAML(first, &fr))

	second, err := marshalYAML(fr)
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))
}

func TestUnmarshalYAMLError(t *testing.T) {
	require.Error(t, unmarshalYAML([]byte("\tnot: [valid"), &fileRequest{}))
}
