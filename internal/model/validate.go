package model

import (
	"errors"
	"fmt"
)

// ValidationError describes a single invariant violation, located by a path through
// the workspace tree (e.g. "collections[0].items[1].request.method").
type ValidationError struct {
	Path string
	Msg  string
}

func (e *ValidationError) Error() string {
	return e.Path + ": " + e.Msg
}

// Validate reports every invariant violation in the workspace, joined into one error
// via errors.Join (nil when valid). Individual causes are *ValidationError and can be
// inspected with errors.As.
func (w *Workspace) Validate() error {
	v := newValidator()
	v.workspace(w)
	return v.result()
}

// Validate checks a collection in isolation (identifiers unique within it).
func (c *Collection) Validate() error {
	v := newValidator()
	v.collection(c, "collection")
	return v.result()
}

// Validate checks a folder in isolation.
func (f *Folder) Validate() error {
	v := newValidator()
	v.folder(f, "folder")
	return v.result()
}

// Validate checks a request in isolation.
func (r *Request) Validate() error {
	v := newValidator()
	v.request(r, "request")
	return v.result()
}

// Validate checks an environment in isolation.
func (e *Environment) Validate() error {
	v := newValidator()
	v.environment(e, "environment")
	return v.result()
}

// validator accumulates violations and tracks identifiers seen so far so uniqueness
// can be enforced across the whole tree in a single pass.
type validator struct {
	errs []error
	seen map[string]string // id -> path where first seen
}

func newValidator() *validator {
	return &validator{seen: map[string]string{}}
}

func (v *validator) result() error {
	return errors.Join(v.errs...)
}

func (v *validator) fail(path, msg string) {
	v.errs = append(v.errs, &ValidationError{Path: path, Msg: msg})
}

// id validates a required, tree-unique identifier at path.
func (v *validator) id(path, id string) {
	if id == "" {
		v.fail(path, "id must not be empty")
		return
	}
	if first, dup := v.seen[id]; dup {
		v.fail(path, fmt.Sprintf("duplicate id %q (first seen at %q)", id, first))
		return
	}
	v.seen[id] = path
}

func (v *validator) workspace(w *Workspace) {
	v.id("workspace", w.ID)
	for i := range w.Collections {
		v.collection(&w.Collections[i], fmt.Sprintf("collections[%d]", i))
	}
	for i := range w.Environments {
		v.environment(&w.Environments[i], fmt.Sprintf("environments[%d]", i))
	}
}

func (v *validator) collection(c *Collection, path string) {
	v.id(path, c.ID)
	v.auth(c.Auth, path+".auth")
	for i := range c.Items {
		v.item(&c.Items[i], fmt.Sprintf("%s.items[%d]", path, i))
	}
}

func (v *validator) folder(f *Folder, path string) {
	v.id(path, f.ID)
	v.auth(f.Auth, path+".auth")
	for i := range f.Items {
		v.item(&f.Items[i], fmt.Sprintf("%s.items[%d]", path, i))
	}
}

func (v *validator) item(it *Item, path string) {
	switch {
	case it.Folder != nil && it.Request != nil:
		v.fail(path, "item must set exactly one of folder or request, not both")
	case it.Folder != nil:
		v.folder(it.Folder, path+".folder")
	case it.Request != nil:
		v.request(it.Request, path+".request")
	default:
		v.fail(path, "item must set folder or request")
	}
}

func (v *validator) request(r *Request, path string) {
	v.id(path, r.ID)
	v.requestShape(r, path)
	for i := range r.Examples {
		ep := fmt.Sprintf("%s.examples[%d]", path, i)
		v.id(ep, r.Examples[i].ID)
		v.requestShape(&r.Examples[i].Request, ep+".request")
	}
}

// requestShape validates the parts of a request that carry no tree-unique id — the
// method, auth, and body. It is reused for saved example snapshots, whose ids are
// intentionally not treated as unique against the live tree.
func (v *validator) requestShape(r *Request, path string) {
	if !ValidMethod(r.Method) {
		v.fail(path+".method", fmt.Sprintf("unknown method %q", r.Method))
	}
	v.auth(r.Auth, path+".auth")
	v.body(r.Body, path+".body")
}

func (v *validator) auth(a *Auth, path string) {
	if a == nil {
		return
	}
	if !a.Type.Valid() {
		v.fail(path+".type", fmt.Sprintf("unknown auth type %q", a.Type))
	}
	set := 0
	for _, present := range []bool{a.Basic != nil, a.Bearer != nil, a.APIKey != nil, a.OAuth2 != nil} {
		if present {
			set++
		}
	}
	if set > 1 {
		v.fail(path, "auth must set at most one credential variant")
	}
}

func (v *validator) body(b *Body, path string) {
	if b == nil {
		return
	}
	if !b.Type.Valid() {
		v.fail(path+".type", fmt.Sprintf("unknown body type %q", b.Type))
		return
	}
	hasText, hasForm := b.Text != "", len(b.Form) > 0
	hasFiles, hasGQL := len(b.Files) > 0, b.GraphQL != nil
	switch b.Type {
	case BodyNone:
		if hasText || hasForm || hasFiles || hasGQL {
			v.fail(path, "body type none must not set any content")
		}
	case BodyJSON, BodyText, BodyXML:
		if hasForm || hasFiles || hasGQL {
			v.fail(path, fmt.Sprintf("body type %q uses text only", b.Type))
		}
	case BodyForm:
		if hasText || hasFiles || hasGQL {
			v.fail(path, "body type form uses form fields only")
		}
	case BodyMultipart:
		if hasText || hasGQL {
			v.fail(path, "body type multipart uses form fields and files only")
		}
	case BodyGraphQL:
		if hasForm || hasFiles {
			v.fail(path, "body type graphql uses text and graphql only")
		}
	case BodyBinary:
		if hasText || hasForm || hasGQL {
			v.fail(path, "body type binary uses files only")
		}
	}
}

func (v *validator) environment(e *Environment, path string) {
	v.id(path, e.ID)
}
