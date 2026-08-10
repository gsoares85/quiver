package vars

import (
	"testing"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

// v builds an enabled variable, which is what almost every fixture wants.
func v(key, value string) model.Variable {
	return model.Variable{Key: key, Value: value, Enabled: true}
}

// nestedWorkspace is a workspace shaped like a real one: a collection holding a request at
// its root and a folder holding a nested folder holding another request. Every level defines
// "target" so precedence has something to decide, and the values name their own level so a
// failure says where the value came from.
func nestedWorkspace() *model.Workspace {
	inner := &model.Folder{
		ID:        "folder-inner",
		Name:      "inner",
		Variables: []model.Variable{v("target", "inner-folder"), v("innerOnly", "yes")},
		Items: []model.Item{
			{Request: &model.Request{ID: "req-deep", Name: "deep", Method: "GET", URL: "https://x/deep"}},
		},
	}
	outer := &model.Folder{
		ID:        "folder-outer",
		Name:      "outer",
		Variables: []model.Variable{v("target", "outer-folder"), v("outerOnly", "yes")},
		Items:     []model.Item{{Folder: inner}},
	}

	return &model.Workspace{
		ID:        "ws",
		Name:      "workspace",
		Variables: []model.Variable{v("target", "workspace"), v("workspaceOnly", "yes")},
		Environments: []model.Environment{
			{ID: "env-staging", Name: "staging", Variables: []model.Variable{v("target", "environment")}},
			{ID: "env-prod", Name: "prod", Variables: []model.Variable{v("target", "prod-environment")}},
		},
		Collections: []model.Collection{{
			ID:        "col",
			Name:      "users-api",
			Variables: []model.Variable{v("target", "collection"), v("collectionOnly", "yes")},
			Items: []model.Item{
				{Request: &model.Request{ID: "req-root", Name: "root", Method: "GET", URL: "https://x/root"}},
				{Folder: outer},
			},
		}},
	}
}

func TestChainPrecedence(t *testing.T) {
	// Scopes are given nearest-first, so peeling one off the front must promote the next.
	overrides := ScopeOf(map[string]string{"target": "override"})
	environment := Scope{v("target", "environment")}
	folder := Scope{v("target", "folder")}
	collection := Scope{v("target", "collection")}
	workspace := Scope{v("target", "workspace")}

	tests := []struct {
		name   string
		scopes []Scope
		want   string
	}{
		{name: "override wins", scopes: []Scope{overrides, environment, folder, collection, workspace}, want: "override"},
		{name: "then environment", scopes: []Scope{environment, folder, collection, workspace}, want: "environment"},
		{name: "then folder", scopes: []Scope{folder, collection, workspace}, want: "folder"},
		{name: "then collection", scopes: []Scope{collection, workspace}, want: "collection"},
		{name: "then workspace", scopes: []Scope{workspace}, want: "workspace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NewChain(tt.scopes...).Lookup("target")
			require.True(t, ok)
			require.Equal(t, tt.want, got.Value)
		})
	}
}

func TestChainSetBeatsEverything(t *testing.T) {
	// A pre-request script setting a variable is the most recent, most explicit statement
	// about it, so it outranks even a --var passed on the command line.
	chain := NewChain(ScopeOf(map[string]string{"target": "override"}), Scope{v("target", "workspace")})

	chain.Set("target", "set-during-run")
	got, ok := chain.Lookup("target")
	require.True(t, ok)
	require.Equal(t, "set-during-run", got.Value)

	// Setting again replaces it, and setting a brand new name works on a chain that had no
	// overlay at all.
	chain.Set("target", "set-again")
	got, _ = chain.Lookup("target")
	require.Equal(t, "set-again", got.Value)

	fresh := NewChain()
	fresh.Set("fromScript", "value")
	got, ok = fresh.Lookup("fromScript")
	require.True(t, ok)
	require.Equal(t, "value", got.Value)
}

func TestChainTreatsDisabledVariablesAsAbsent(t *testing.T) {
	// Switching a variable off means the user wanted it gone — not overridden with nothing.
	// A disabled entry in a nearer scope must therefore let the farther one through.
	chain := NewChain(
		Scope{{Key: "target", Value: "near-but-off", Enabled: false}},
		Scope{v("target", "far-and-on")},
	)

	got, ok := chain.Lookup("target")
	require.True(t, ok)
	require.Equal(t, "far-and-on", got.Value)

	// Disabled everywhere is simply undefined, which is what surfaces it to the user.
	off := NewChain(Scope{{Key: "target", Value: "off", Enabled: false}})
	_, ok = off.Lookup("target")
	require.False(t, ok)
}

func TestChainLookupReportsTheWholeVariable(t *testing.T) {
	// A secret carries no value in the file; the caller has to see the flag to know it must
	// fetch one, which is why Lookup hands back the variable rather than a string.
	chain := NewChain(Scope{{Key: "apiToken", Secret: true, Enabled: true}})

	got, ok := chain.Lookup("apiToken")
	require.True(t, ok)
	require.True(t, got.Secret)
	require.Empty(t, got.Value)
	require.Equal(t, "apiToken", got.Key)
}

func TestChainFirstEntryInAScopeWins(t *testing.T) {
	chain := NewChain(Scope{v("target", "first"), v("target", "second")})

	got, ok := chain.Lookup("target")
	require.True(t, ok)
	require.Equal(t, "first", got.Value)
}

func TestChainUnknownName(t *testing.T) {
	_, ok := NewChain(Scope{v("target", "x")}).Lookup("nothingDefinesThis")
	require.False(t, ok)
}

func TestScopeOfIsStable(t *testing.T) {
	scope := ScopeOf(map[string]string{"zebra": "1", "alpha": "2", "middle": "3"})

	require.Equal(t, Scope{v("alpha", "2"), v("middle", "3"), v("zebra", "1")}, scope,
		"a scope built from a map is sorted, so it is the same scope every time")
	require.Empty(t, ScopeOf(nil))
}

func TestAncestryOf(t *testing.T) {
	ws := nestedWorkspace()

	tests := []struct {
		name        string
		requestID   string
		wantFolders []string // innermost first
	}{
		{name: "request at the collection root", requestID: "req-root"},
		{name: "request inside nested folders", requestID: "req-deep", wantFolders: []string{"folder-inner", "folder-outer"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AncestryOf(ws, tt.requestID)
			require.NoError(t, err)

			require.Equal(t, tt.requestID, got.Request.ID)
			require.Equal(t, "col", got.Collection.ID)

			var ids []string
			for _, folder := range got.Folders {
				ids = append(ids, folder.ID)
			}
			require.Equal(t, tt.wantFolders, ids, "folders come back innermost first")
		})
	}
}

func TestAncestryOfMissingRequest(t *testing.T) {
	ws := nestedWorkspace()

	tests := []struct {
		name      string
		ws        *model.Workspace
		requestID string
	}{
		{name: "no such id", ws: ws, requestID: "req-nope"},
		{name: "empty id", ws: ws, requestID: ""},
		{name: "no workspace", ws: nil, requestID: "req-root"},
		{name: "empty workspace", ws: &model.Workspace{}, requestID: "req-root"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := AncestryOf(tt.ws, tt.requestID)
			require.ErrorIs(t, err, ErrRequestNotFound)
		})
	}
}

func TestChainForAssemblesTheWholeCascade(t *testing.T) {
	ws := nestedWorkspace()
	env, err := EnvironmentByName(ws, "staging")
	require.NoError(t, err)

	chain, err := ChainFor(ws, "req-deep", env, map[string]string{"target": "override"})
	require.NoError(t, err)

	// Every level defines "target"; the override is nearest, so it wins.
	got, ok := chain.Lookup("target")
	require.True(t, ok)
	require.Equal(t, "override", got.Value)

	// Names defined at only one level all resolve, which proves every scope made it in and
	// in the right order.
	for name, want := range map[string]string{
		"innerOnly":      "yes",
		"outerOnly":      "yes",
		"collectionOnly": "yes",
		"workspaceOnly":  "yes",
	} {
		got, ok := chain.Lookup(name)
		require.Truef(t, ok, "%s should resolve", name)
		require.Equal(t, want, got.Value)
	}
}

func TestChainForWithoutOverridesOrEnvironment(t *testing.T) {
	ws := nestedWorkspace()

	// No override and no environment: the innermost folder is then nearest.
	chain, err := ChainFor(ws, "req-deep", nil, nil)
	require.NoError(t, err)
	got, ok := chain.Lookup("target")
	require.True(t, ok)
	require.Equal(t, "inner-folder", got.Value)

	// The same request with an environment selected: the environment outranks the folders.
	env, err := EnvironmentByName(ws, "prod")
	require.NoError(t, err)
	chain, err = ChainFor(ws, "req-deep", env, nil)
	require.NoError(t, err)
	got, _ = chain.Lookup("target")
	require.Equal(t, "prod-environment", got.Value)

	// A request at the collection root has no folders between it and its collection.
	chain, err = ChainFor(ws, "req-root", nil, nil)
	require.NoError(t, err)
	got, _ = chain.Lookup("target")
	require.Equal(t, "collection", got.Value)
}

func TestChainForMissingRequest(t *testing.T) {
	chain, err := ChainFor(nestedWorkspace(), "req-nope", nil, nil)
	require.Nil(t, chain)
	require.ErrorIs(t, err, ErrRequestNotFound)
}

func TestEnvironmentByName(t *testing.T) {
	ws := nestedWorkspace()

	env, err := EnvironmentByName(ws, "staging")
	require.NoError(t, err)
	require.Equal(t, "env-staging", env.ID)

	// The error names what is available, because that is the part a user can act on.
	_, err = EnvironmentByName(ws, "Staging")
	require.ErrorIs(t, err, ErrEnvironmentNotFound)
	require.ErrorContains(t, err, `find environment "Staging"`)
	require.ErrorContains(t, err, "available: prod, staging")

	_, err = EnvironmentByName(&model.Workspace{}, "staging")
	require.ErrorIs(t, err, ErrEnvironmentNotFound)
	require.ErrorContains(t, err, "available: none defined")

	_, err = EnvironmentByName(nil, "staging")
	require.ErrorIs(t, err, ErrEnvironmentNotFound)
}
