package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// validWorkspace returns a minimal workspace that passes Validate.
func validWorkspace() *Workspace {
	return &Workspace{
		SchemaVersion: 1,
		ID:            "ws",
		Name:          "ws",
		Environments:  []Environment{{ID: "env", Name: "local"}},
		Collections: []Collection{{
			ID:   "col",
			Name: "col",
			Items: []Item{{Folder: &Folder{
				ID:   "fld",
				Name: "f",
				Items: []Item{{Request: &Request{
					ID: "req", Name: "r", Method: "GET", URL: "u",
				}}},
			}}},
		}},
	}
}

// theRequest is a shortcut to the single request in validWorkspace's tree.
func theRequest(w *Workspace) *Request {
	return w.Collections[0].Items[0].Folder.Items[0].Request
}

const reqPath = "collections[0].items[0].folder.items[0].request"

// validationErrors unwraps the joined error into its *ValidationError causes.
func validationErrors(t *testing.T, err error) []*ValidationError {
	t.Helper()
	require.Error(t, err)
	joined, ok := err.(interface{ Unwrap() []error })
	require.True(t, ok, "expected a joined error")
	out := make([]*ValidationError, 0, len(joined.Unwrap()))
	for _, e := range joined.Unwrap() {
		var ve *ValidationError
		require.True(t, errors.As(e, &ve), "cause should be *ValidationError")
		out = append(out, ve)
	}
	return out
}

func TestValidateValidWorkspace(t *testing.T) {
	require.NoError(t, validWorkspace().Validate())
}

func TestValidateAcceptsEveryBodyAndAuthVariant(t *testing.T) {
	bodies := []*Body{
		{Type: BodyNone},
		{Type: BodyJSON, Text: "{}"},
		{Type: BodyText, Text: "hi"},
		{Type: BodyXML, Text: "<x/>"},
		{Type: BodyForm, Form: []Param{{Key: "a", Value: "b", Enabled: true}}},
		{Type: BodyMultipart, Form: []Param{{Key: "a"}}, Files: []FileRef{{Path: "/f"}}},
		{Type: BodyGraphQL, Text: "query{}", GraphQL: &GraphQL{OperationName: "q"}},
		{Type: BodyBinary, Files: []FileRef{{Path: "/f"}}},
	}
	auths := []*Auth{
		nil,
		{Type: AuthNone},
		{Type: AuthBasic, Basic: &BasicAuth{Username: "u"}},
		{Type: AuthBearer, Bearer: &BearerAuth{Token: "t"}},
		{Type: AuthAPIKey, APIKey: &APIKeyAuth{Key: "k", In: "header"}},
		{Type: AuthOAuth2, OAuth2: &OAuth2Auth{}},
	}

	ws := &Workspace{ID: "ws", Collections: []Collection{{ID: "col"}}}
	n := 0
	for _, b := range bodies {
		for _, a := range auths {
			n++
			ws.Collections[0].Items = append(ws.Collections[0].Items, Item{Request: &Request{
				ID: string(rune('a'+n%26)) + string(rune('0'+n/26)), Method: "POST", Body: b, Auth: a,
			}})
		}
	}
	require.NoError(t, ws.Validate())
}

func TestValidateRules(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Workspace)
		path     string
		contains string
	}{
		{"empty workspace id", func(w *Workspace) { w.ID = "" }, "workspace", "id must not be empty"},
		{"empty collection id", func(w *Workspace) { w.Collections[0].ID = "" }, "collections[0]", "id must not be empty"},
		{"duplicate id", func(w *Workspace) { w.Collections[0].ID = "ws" }, "collections[0]", "duplicate id"},
		{"empty folder id", func(w *Workspace) { w.Collections[0].Items[0].Folder.ID = "" }, "collections[0].items[0].folder", "id must not be empty"},
		{"empty request id", func(w *Workspace) { theRequest(w).ID = "" }, reqPath, "id must not be empty"},
		{"empty environment id", func(w *Workspace) { w.Environments[0].ID = "" }, "environments[0]", "id must not be empty"},
		{"unknown method", func(w *Workspace) { theRequest(w).Method = "FETCH" }, reqPath + ".method", "unknown method"},
		{"unknown auth type", func(w *Workspace) { theRequest(w).Auth = &Auth{Type: "digest"} }, reqPath + ".auth.type", "unknown auth type"},
		{"two auth variants", func(w *Workspace) {
			theRequest(w).Auth = &Auth{Type: AuthBearer, Basic: &BasicAuth{}, Bearer: &BearerAuth{}}
		}, reqPath + ".auth", "at most one credential variant"},
		{"auth requires its named variant", func(w *Workspace) {
			theRequest(w).Auth = &Auth{Type: AuthBasic}
		}, reqPath + ".auth", "requires basic credentials"},
		{"auth wrong variant for type", func(w *Workspace) {
			theRequest(w).Auth = &Auth{Type: AuthBasic, Bearer: &BearerAuth{}}
		}, reqPath + ".auth", "must set basic credentials, not bearer"},
		{"auth none must not carry a payload", func(w *Workspace) {
			theRequest(w).Auth = &Auth{Type: AuthNone, Basic: &BasicAuth{}}
		}, reqPath + ".auth", "must not set basic credentials"},
		{"unknown body type", func(w *Workspace) { theRequest(w).Body = &Body{Type: "protobuf"} }, reqPath + ".body.type", "unknown body type"},
		{"none body with content", func(w *Workspace) { theRequest(w).Body = &Body{Type: BodyNone, Text: "x"} }, reqPath + ".body", "must not set any content"},
		{"json body with form", func(w *Workspace) {
			theRequest(w).Body = &Body{Type: BodyJSON, Form: []Param{{Key: "a"}}}
		}, reqPath + ".body", "uses text only"},
		{"form body with files", func(w *Workspace) {
			theRequest(w).Body = &Body{Type: BodyForm, Files: []FileRef{{Path: "/f"}}}
		}, reqPath + ".body", "form fields only"},
		{"multipart body with text", func(w *Workspace) {
			theRequest(w).Body = &Body{Type: BodyMultipart, Text: "x"}
		}, reqPath + ".body", "form fields and files only"},
		{"graphql body with files", func(w *Workspace) {
			theRequest(w).Body = &Body{Type: BodyGraphQL, Files: []FileRef{{Path: "/f"}}}
		}, reqPath + ".body", "text and graphql only"},
		{"binary body with text", func(w *Workspace) {
			theRequest(w).Body = &Body{Type: BodyBinary, Text: "x"}
		}, reqPath + ".body", "files only"},
		{"item with both folder and request", func(w *Workspace) {
			w.Collections[0].Items[0].Request = &Request{ID: "extra", Method: "GET"}
		}, "collections[0].items[0]", "not both"},
		{"item with neither", func(w *Workspace) {
			w.Collections[0].Items = []Item{{}}
		}, "collections[0].items[0]", "must set folder or request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := validWorkspace()
			tt.mutate(ws)
			errs := validationErrors(t, ws.Validate())
			require.Len(t, errs, 1)
			require.Equal(t, tt.path, errs[0].Path)
			require.Contains(t, errs[0].Msg, tt.contains)
		})
	}
}

func TestValidateCollectsAllErrors(t *testing.T) {
	ws := validWorkspace()
	ws.ID = ""                     // one violation
	theRequest(ws).Method = "NOPE" // another
	errs := validationErrors(t, ws.Validate())
	require.Len(t, errs, 2)
}

func TestValidateExampleSnapshot(t *testing.T) {
	ws := validWorkspace()
	theRequest(ws).Examples = []Example{
		{ID: "", Request: Request{ID: "snap", Method: "GET"}},      // empty example id
		{ID: "ex2", Request: Request{ID: "snap", Method: "BOGUS"}}, // snapshot bad method
	}
	errs := validationErrors(t, ws.Validate())
	require.Len(t, errs, 2)
	// the snapshot's own id ("snap") is NOT treated as a duplicate/uniqueness error
	for _, e := range errs {
		require.NotContains(t, e.Msg, "duplicate")
	}
}

func TestSubAggregateValidate(t *testing.T) {
	require.NoError(t, (&Collection{ID: "c"}).Validate())
	require.Error(t, (&Collection{ID: ""}).Validate())

	require.NoError(t, (&Folder{ID: "f"}).Validate())
	require.Error(t, (&Folder{ID: ""}).Validate())

	require.NoError(t, (&Request{ID: "r", Method: "GET"}).Validate())
	require.Error(t, (&Request{ID: "r", Method: "bad"}).Validate())

	require.NoError(t, (&Environment{ID: "e"}).Validate())
	require.Error(t, (&Environment{ID: ""}).Validate())
}

func TestValidationErrorString(t *testing.T) {
	e := &ValidationError{Path: "collections[0]", Msg: "id must not be empty"}
	require.Equal(t, "collections[0]: id must not be empty", e.Error())
}
