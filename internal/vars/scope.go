package vars

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gsoares85/quiver/internal/model"
)

// Sentinel errors so a caller can tell "you asked for something that is not there" apart
// from a resolution failure.
var (
	// ErrRequestNotFound reports that a workspace holds no request with the given id.
	ErrRequestNotFound = errors.New("request not found in workspace")

	// ErrEnvironmentNotFound reports that a workspace defines no environment by that name.
	ErrEnvironmentNotFound = errors.New("environment not found in workspace")
)

// Scope is one set of variables in the cascade: an environment, a folder, a collection, the
// workspace, or the values a caller passed in. Within a scope the first entry to match a
// name wins, so a file with the same key twice behaves predictably.
type Scope []model.Variable

// ScopeOf turns plain key/values — the `--var` flags, say — into a scope. Every entry is
// enabled: a value passed explicitly was not switched off. Keys are sorted so a scope built
// from a map is the same scope every time.
func ScopeOf(values map[string]string) Scope {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	scope := make(Scope, 0, len(keys))
	for _, key := range keys {
		scope = append(scope, model.Variable{Key: key, Value: values[key], Enabled: true})
	}
	return scope
}

// Chain is the ordered set of scopes a request resolves against, nearest first. The first
// scope that defines a name wins; see the package doc for the full order.
//
// A chain belongs to one run and is not safe for concurrent use — Set exists precisely so a
// pre-request script can change what the rest of that run sees.
type Chain struct {
	overlay map[string]string // set during the run; above every stored scope
	scopes  []Scope
}

// NewChain builds a chain from scopes given nearest-first. ChainFor is the usual way in;
// this is for a caller that already knows its scopes.
func NewChain(scopes ...Scope) *Chain {
	return &Chain{scopes: scopes}
}

// ChainFor assembles the chain for one request in a workspace: the caller's overrides, the
// active environment, the folders the request sits in from the inside out, its collection,
// and finally the workspace. env and overrides may be nil when there is no environment
// selected or nothing was overridden.
//
// Building the chain here rather than in each front-end is the point of the package: if the
// app and the CLI each assembled their own, precedence would exist twice and would
// eventually disagree.
func ChainFor(ws *model.Workspace, requestID string, env *model.Environment, overrides map[string]string) (*Chain, error) {
	ancestry, err := AncestryOf(ws, requestID)
	if err != nil {
		return nil, err
	}

	scopes := make([]Scope, 0, len(ancestry.Folders)+3)
	if len(overrides) > 0 {
		scopes = append(scopes, ScopeOf(overrides))
	}
	if env != nil {
		scopes = append(scopes, env.Variables)
	}
	for _, folder := range ancestry.Folders {
		scopes = append(scopes, folder.Variables)
	}
	scopes = append(scopes, ancestry.Collection.Variables, ws.Variables)

	return &Chain{scopes: scopes}, nil
}

// Set records a variable for the rest of the run, above every stored scope. Pre-request
// scripts write here; nothing about it is persisted.
func (c *Chain) Set(name, value string) {
	if c.overlay == nil {
		c.overlay = make(map[string]string, 1)
	}
	c.overlay[name] = value
}

// Lookup returns the variable the chain resolves name to. A variable switched off
// (Enabled false) is treated as absent — it does not shadow the same name in a farther
// scope, because "disabled" means the user wanted it gone, not overridden with nothing.
//
// It returns the whole variable rather than its value so a caller can see that a name is
// defined as a secret, whose value lives outside the file and has to be fetched.
func (c *Chain) Lookup(name string) (model.Variable, bool) {
	if value, ok := c.overlay[name]; ok {
		return model.Variable{Key: name, Value: value, Enabled: true}, true
	}
	for _, scope := range c.scopes {
		for i := range scope {
			if scope[i].Key == name && scope[i].Enabled {
				return scope[i], true
			}
		}
	}
	return model.Variable{}, false
}

// Ancestry is where a request sits in a workspace: the folders around it from the inside
// out, and the collection they belong to.
//
// It is exported, and separate from ChainFor, because auth inheritance needs exactly this
// walk. Two walks of the same tree are two chances for the answers to disagree.
type Ancestry struct {
	Collection *model.Collection
	Folders    []*model.Folder // innermost first, matching precedence order
	Request    *model.Request
}

// AncestryOf finds the request with the given id and reports what surrounds it.
func AncestryOf(ws *model.Workspace, requestID string) (Ancestry, error) {
	notFound := func() (Ancestry, error) {
		return Ancestry{}, fmt.Errorf("find request %q: %w", requestID, ErrRequestNotFound)
	}
	if ws == nil || requestID == "" {
		return notFound()
	}

	for i := range ws.Collections {
		collection := &ws.Collections[i]
		if found, ok := findRequest(collection.Items, requestID, nil); ok {
			found.Collection = collection
			return found, nil
		}
	}
	return notFound()
}

// findRequest searches items depth-first, carrying the folders walked through so far.
func findRequest(items []model.Item, requestID string, trail []*model.Folder) (Ancestry, bool) {
	for i := range items {
		item := &items[i]
		switch {
		case item.Request != nil:
			if item.Request.ID == requestID {
				// Reverse into precedence order, and copy: the trail's backing array is
				// reused by the sibling branches still to be searched.
				folders := slices.Clone(trail)
				slices.Reverse(folders)
				return Ancestry{Folders: folders, Request: item.Request}, true
			}
		case item.Folder != nil:
			if found, ok := findRequest(item.Folder.Items, requestID, append(trail, item.Folder)); ok {
				return found, true
			}
		}
	}
	return Ancestry{}, false
}

// EnvironmentByName finds an environment by its exact name. The match is case-sensitive,
// like the rest of the file format; the error lists what is available, because "no such
// environment" on its own is the least useful thing a CLI can say.
func EnvironmentByName(ws *model.Workspace, name string) (*model.Environment, error) {
	if ws != nil {
		for i := range ws.Environments {
			if ws.Environments[i].Name == name {
				return &ws.Environments[i], nil
			}
		}
	}

	available := "none defined"
	if ws != nil && len(ws.Environments) > 0 {
		names := make([]string, 0, len(ws.Environments))
		for i := range ws.Environments {
			names = append(names, ws.Environments[i].Name)
		}
		slices.Sort(names)
		available = strings.Join(names, ", ")
	}
	return nil, fmt.Errorf("find environment %q: %w (available: %s)", name, ErrEnvironmentNotFound, available)
}
