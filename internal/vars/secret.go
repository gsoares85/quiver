package vars

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
)

// Mask is what a redacted secret reads as. It is exported so a front-end can recognise —
// and style — a value that has been masked rather than lost.
const Mask = "[redacted]"

// SecretSource supplies the value of a variable that files store as a reference only. Where
// that value comes from — an explicit override, the OS keychain, a one-time prompt — is the
// source's business.
//
// The interface is declared here, beside its only caller, and implemented elsewhere: that is
// what keeps knowledge of credential stores out of every other package, and lets every test
// in this one run against a value in memory.
type SecretSource interface {
	// Secret returns the value stored under key. found is false when the source simply has
	// no value for it, which the resolver treats as an unresolved variable — something the
	// user must supply — rather than as a failure. An error means the source could not
	// answer at all: a locked keychain, a refused prompt.
	Secret(ctx context.Context, key string) (value string, found bool, err error)
}

// Secrets is the set of secret values a resolution substituted into a request, together with
// the encoded forms those values take on the wire. Once substituted, a secret is
// indistinguishable from any other string, so this is the only record of what needs masking
// before anything derived from the request — a log line, a console dump, a saved response —
// leaves the process.
type Secrets struct {
	values []string
}

// Values lists what needs masking, in the order it was first recorded: the values that were
// substituted, and any encoding of them that reaches the wire in place of the value itself.
// The slice is a copy, but it holds the real secrets: handle it as carefully as the
// credentials themselves.
func (s Secrets) Values() []string {
	return slices.Clone(s.values)
}

// Redact replaces every secret value in text with Mask.
//
// Longer values are replaced first, so a secret that happens to contain another does not
// leave the rest of itself behind in the output.
func (s Secrets) Redact(text string) string {
	if len(s.values) == 0 || text == "" {
		return text
	}

	ordered := slices.Clone(s.values)
	slices.SortFunc(ordered, func(a, b string) int { return len(b) - len(a) })

	pairs := make([]string, 0, len(ordered)*2)
	for _, value := range ordered {
		pairs = append(pairs, value, Mask)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// add records a substituted secret value. An empty value is ignored: masking it would put
// Mask between every character of every string it was asked to clean.
func (s *Secrets) add(value string) {
	if value == "" || slices.Contains(s.values, value) {
		return
	}
	s.values = append(s.values, value)
}

// addBasic records the credential an "Authorization: Basic" header will carry for a pair
// where either half was a secret. The header value is base64 of "user:password", which
// contains neither half literally, so masking the substituted values alone would leave a
// credential that decodes straight back to both in any header or wire dump.
//
// The encoding deliberately mirrors http.Request.SetBasicAuth, which is what the request
// engine uses. Nothing in the compiler holds the two together — this package stays a leaf
// that never imports the engine — so a change there has to be followed here.
//
// Only the encoded payload is recorded and not the "Basic " prefix, so it masks the same
// whether a dump shows the whole header line or the blob on its own.
func (s *Secrets) addBasic(username, password string) {
	// Both halves empty means nothing of substance was substituted, and the encoding of a
	// bare ":" is not a credential: recording it would mask four characters of any text.
	if username == "" && password == "" {
		return
	}
	s.add(base64.StdEncoding.EncodeToString([]byte(username + ":" + password)))
}

// resolution is the state of a single resolve: the chain names are looked up in, where
// secret values come from, and the secrets substituted so far. It holds a context because
// the expander it feeds looks variables up by name alone, and fetching a secret is I/O that
// a cancelled run must be able to abandon.
type resolution struct {
	ctx     context.Context
	chain   *Chain
	source  SecretSource
	secrets Secrets

	// secretUses counts secret substitutions, repeats included. The recorded set cannot
	// answer "did these fields consume a secret?" on its own: it holds each value once, so
	// a credential already used by an earlier field leaves it unchanged.
	secretUses int
}

// useSecret records a substituted secret value and counts the use.
func (r *resolution) useSecret(value string) {
	r.secrets.add(value)
	r.secretUses++
}

// lookup is what the expander calls for every reference. A plain variable answers from the
// file; a secret has no value in the file by design, so its value is fetched and recorded.
func (r *resolution) lookup(name string) (string, bool, error) {
	// Every reference passes through here, which makes this the one place a cancelled run
	// can be noticed cheaply — before a source that would happily sit on a prompt is asked,
	// and often enough that a long expansion stops rather than running to its budget.
	if err := r.ctx.Err(); err != nil {
		return "", false, err
	}

	// A variable set during the run already holds its value, secret or not. Recording it
	// here is what lets a token a script obtained be masked like any other secret.
	if v, ok := r.chain.runtime(name); ok {
		if v.secret {
			r.useSecret(v.value)
		}
		return v.value, true, nil
	}

	variable, ok := r.chain.Lookup(name)
	if !ok {
		return "", false, nil
	}
	if !variable.Secret {
		return variable.Value, true, nil
	}

	if r.source == nil {
		return "", false, fmt.Errorf("variable %q is a secret and no secret source was provided", variable.Key)
	}

	// Deliberately the source's value and not the variable's own: a secret's value is not
	// supposed to be in the file, and honouring one that is there would make "secrets never
	// live in the files" a promise that quietly depends on the file.
	value, found, err := r.source.Secret(r.ctx, variable.Key)
	if err != nil {
		return "", false, fmt.Errorf("read secret: %w", err)
	}
	if !found {
		return "", false, nil // unresolved: the user has to supply it
	}

	r.useSecret(value)
	return value, true, nil
}
