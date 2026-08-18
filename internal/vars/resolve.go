package vars

import (
	"context"
	"strings"

	"github.com/gsoares85/quiver/internal/model"
)

// Resolved is a request with every reference substituted, ready to be sent, together with
// the secret values that went into it so they can be masked afterwards.
type Resolved struct {
	Request model.Request
	Secrets Secrets
}

// UnresolvedError reports references that nothing defined. Every name in the request is
// listed, because fixing three missing variables should take one round trip and not three.
type UnresolvedError struct {
	Names []string // in the order they appear in the request
}

func (e *UnresolvedError) Error() string {
	quoted := make([]string, 0, len(e.Names))
	for _, name := range e.Names {
		quoted = append(quoted, refOpen+name+refClose)
	}
	if len(e.Names) == 1 {
		return "unresolved variable: " + quoted[0]
	}
	return "unresolved variables: " + strings.Join(quoted, ", ")
}

// Resolve substitutes every variable reference in req against chain and returns the result.
// The request passed in is never modified: callers hold the stored entity, and a resolution
// is a throwaway copy of it for one send.
//
// What is substituted: the URL; the keys and values of headers, query parameters, and form
// fields; the body text and a GraphQL operation's variables; referenced upload paths; and
// every credential field. Entries switched off are dropped, so what comes back is exactly
// what should go on the wire.
//
// What is not: the method, an API key's placement, an OAuth grant type — fields that name a
// fixed choice rather than carry a value — and the pre-request and test scripts, which reach
// variables through the scripting API instead. Substituting into a script would be splicing
// text into code. Saved examples are dropped rather than resolved: they document the request,
// they are not part of sending it.
//
// A name nothing defines is reported as an *UnresolvedError listing all of them; a variable
// that refers back to itself as a *CycleError; a secret the source could not produce as that
// source's own error. chain may be nil, which simply means nothing is defined.
func Resolve(ctx context.Context, req model.Request, chain *Chain, source SecretSource) (Resolved, error) {
	if chain == nil {
		chain = NewChain()
	}
	state := &resolution{ctx: ctx, chain: chain, source: source}
	e := newExpander(state.lookup)

	// Fields are walked in the order someone reads a request — URL, headers, query, auth,
	// body — so the unresolved report comes back in the order they would scan for them.
	out := req

	// Saved examples are documentation, not something to send, and keeping them would leave
	// the copy sharing the stored request's slice. Dropping them settles both at once.
	out.Examples = nil

	if err := expandAll(e, &out.URL); err != nil {
		return Resolved{}, err
	}

	var err error
	if out.Headers, err = resolveParams(e, req.Headers); err != nil {
		return Resolved{}, err
	}
	if out.Query, err = resolveParams(e, req.Query); err != nil {
		return Resolved{}, err
	}
	if out.Auth, err = state.resolveAuth(e, req.Auth); err != nil {
		return Resolved{}, err
	}
	if out.Body, err = resolveBody(e, req.Body); err != nil {
		return Resolved{}, err
	}

	if names := e.unresolvedNames(); len(names) > 0 {
		return Resolved{}, &UnresolvedError{Names: names}
	}
	return Resolved{Request: out, Secrets: state.secrets}, nil
}

// expandAll substitutes into every string it is handed, stopping at the first hard failure —
// a cycle, or a secret source that could not answer. An unresolved name is not a hard
// failure: it is collected so one pass can report all of them.
func expandAll(e *expander, fields ...*string) error {
	for _, field := range fields {
		expanded, err := e.expand(*field)
		if err != nil {
			return err
		}
		*field = expanded
	}
	return nil
}

// resolveParams substitutes into each entry's key and value and drops the ones switched off.
// Keys are substituted as well as values, or a {{tenant}} in a header name would reach the
// wire as those literal characters.
//
// Dropping disabled entries belongs here because this is the one place that owns the
// decision. The request engine deliberately does not repeat it: Enabled's zero value is
// false, so a second filter downstream would silently discard a hand-built entry.
func resolveParams(e *expander, in []model.Header) ([]model.Header, error) {
	if in == nil {
		return nil, nil // a request with no headers keeps having no headers
	}
	out := make([]model.Header, 0, len(in))
	for _, entry := range in { // a copy per iteration, so the caller's slice is untouched
		if !entry.Enabled {
			continue
		}
		if err := expandAll(e, &entry.Key, &entry.Value); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

// resolveAuth substitutes into whichever credentials the request carries. Each variant is
// copied before being written to, so the stored auth is left as it was.
//
// It is a method rather than a free function because basic credentials need one thing the
// expander cannot report: whether a secret went into them. See the Basic branch.
func (r *resolution) resolveAuth(e *expander, in *model.Auth) (*model.Auth, error) {
	if in == nil {
		return nil, nil
	}
	out := *in

	if in.Basic != nil {
		basic := *in.Basic
		uses := r.secretUses
		if err := expandAll(e, &basic.Username, &basic.Password); err != nil {
			return nil, err
		}
		// A basic credential does not reach the wire as either half: the engine sends
		// base64("user:password"), which contains neither literally. Recording the encoding
		// alongside the values is what keeps the credential masked in a header dump instead
		// of travelling through Redact untouched and decoding back to both halves.
		//
		// Only when a secret was actually substituted here: encoding a pair of ordinary
		// variables would mask text that was never a secret.
		if r.secretUses > uses {
			r.secrets.addBasic(basic.Username, basic.Password)
		}
		out.Basic = &basic
	}
	if in.Bearer != nil {
		bearer := *in.Bearer
		if err := expandAll(e, &bearer.Token); err != nil {
			return nil, err
		}
		out.Bearer = &bearer
	}
	if in.APIKey != nil {
		apiKey := *in.APIKey
		if err := expandAll(e, &apiKey.Key, &apiKey.Value); err != nil {
			return nil, err
		}
		out.APIKey = &apiKey
	}
	if in.OAuth2 != nil {
		oauth2 := *in.OAuth2
		if err := expandAll(e,
			&oauth2.AuthURL, &oauth2.TokenURL, &oauth2.ClientID, &oauth2.ClientSecret,
			&oauth2.Scope, &oauth2.AccessToken, &oauth2.RefreshToken,
		); err != nil {
			return nil, err
		}
		out.OAuth2 = &oauth2
	}
	return &out, nil
}

// resolveBody substitutes into the body, whichever shape it takes.
func resolveBody(e *expander, in *model.Body) (*model.Body, error) {
	if in == nil {
		return nil, nil
	}
	out := *in
	if err := expandAll(e, &out.Text); err != nil {
		return nil, err
	}

	var err error
	if out.Form, err = resolveParams(e, in.Form); err != nil {
		return nil, err
	}

	if in.Files != nil {
		files := make([]model.FileRef, 0, len(in.Files))
		for _, ref := range in.Files {
			// Paths are substituted too: a {{fixtures}}/logo.png that stayed literal would
			// fail later as a missing file, which says nothing about the real problem.
			if err := expandAll(e, &ref.Field, &ref.Path); err != nil {
				return nil, err
			}
			files = append(files, ref)
		}
		out.Files = files
	}
	if in.GraphQL != nil {
		graphql := *in.GraphQL
		if err := expandAll(e, &graphql.Variables, &graphql.OperationName); err != nil {
			return nil, err
		}
		out.GraphQL = &graphql
	}
	return &out, nil
}
