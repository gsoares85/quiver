package vars

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoares85/quiver/internal/model"
	"github.com/stretchr/testify/require"
)

// fakeSource stands in for a keychain. It records what it was asked and the context it was
// given, so a test can prove the resolver both consults it and does not consult it.
type fakeSource struct {
	values map[string]string
	err    error

	asked     []string
	gotCtx    context.Context
	callCount int
}

func (f *fakeSource) Secret(ctx context.Context, key string) (string, bool, error) {
	f.callCount++
	f.asked = append(f.asked, key)
	f.gotCtx = ctx
	if f.err != nil {
		return "", false, f.err
	}
	value, ok := f.values[key]
	return value, ok, nil
}

// secretVar is a variable stored the way a secret is: a key, no value.
func secretVar(key string) model.Variable {
	return model.Variable{Key: key, Secret: true, Enabled: true}
}

// newResolution wires a chain and a source together the way Resolve will.
func newResolution(t *testing.T, ctx context.Context, chain *Chain, source SecretSource) *resolution {
	t.Helper()
	return &resolution{ctx: ctx, chain: chain, source: source}
}

func TestResolutionLookupPlainVariable(t *testing.T) {
	source := &fakeSource{values: map[string]string{"baseUrl": "should never be asked for"}}
	r := newResolution(t, context.Background(), NewChain(Scope{v("baseUrl", "https://api.example.com")}), source)

	value, found, err := r.lookup("baseUrl")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "https://api.example.com", value)

	require.Zero(t, source.callCount, "an ordinary variable must never reach the secret source")
	require.Empty(t, r.secrets.Values(), "and must not be recorded as a secret")
}

func TestResolutionLookupUndefinedName(t *testing.T) {
	// A name no scope defines is not a secret the source might know about: the chain is the
	// only place names come from, so asking a keychain about an unknown one would be both
	// pointless and a way to leak what a workspace refers to.
	source := &fakeSource{values: map[string]string{"nothingDefinesThis": "surprise"}}
	r := newResolution(t, context.Background(), NewChain(Scope{v("baseUrl", "https://x")}), source)

	value, found, err := r.lookup("nothingDefinesThis")
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, value)
	require.Zero(t, source.callCount)
}

func TestResolutionLookupSecret(t *testing.T) {
	const token = "sk-live-abc123"
	source := &fakeSource{values: map[string]string{"apiToken": token}}
	r := newResolution(t, context.Background(), NewChain(Scope{secretVar("apiToken")}), source)

	value, found, err := r.lookup("apiToken")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, token, value)

	require.Equal(t, []string{"apiToken"}, source.asked)
	require.Equal(t, []string{token}, r.secrets.Values(), "a substituted secret is recorded for masking")
}

func TestResolutionIgnoresAValueStoredOnASecret(t *testing.T) {
	// A secret's value is not supposed to be in the file. If one is there — hand-edited, or
	// written by something older — using it would make "secrets never live in the files" a
	// promise that quietly depends on the file, so the source is the only answer accepted.
	const fromSource = "value-from-keychain"
	decoy := model.Variable{Key: "apiToken", Value: "value-committed-by-mistake", Secret: true, Enabled: true}

	source := &fakeSource{values: map[string]string{"apiToken": fromSource}}
	r := newResolution(t, context.Background(), NewChain(Scope{decoy}), source)

	value, found, err := r.lookup("apiToken")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, fromSource, value)
	require.NotContains(t, r.secrets.Values(), decoy.Value)
}

func TestResolutionSecretWithNoValueIsUnresolved(t *testing.T) {
	// Nothing to substitute is not a failure: it is a variable the user still has to supply,
	// which is exactly what an unresolved reference reports.
	source := &fakeSource{values: map[string]string{}}
	r := newResolution(t, context.Background(), NewChain(Scope{secretVar("apiToken")}), source)

	value, found, err := r.lookup("apiToken")
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, value)
	require.Empty(t, r.secrets.Values())
}

func TestResolutionReportsASourceFailure(t *testing.T) {
	const token = "sk-live-do-not-log"
	locked := errors.New("keychain is locked")
	source := &fakeSource{values: map[string]string{"apiToken": token}, err: locked}
	r := newResolution(t, context.Background(), NewChain(Scope{secretVar("apiToken")}), source)

	_, found, err := r.lookup("apiToken")
	require.False(t, found)
	require.ErrorIs(t, err, locked)
	require.ErrorContains(t, err, "read secret")
	require.NotContains(t, err.Error(), token, "a failure must never carry the credential")
}

func TestResolutionWithoutASource(t *testing.T) {
	// A secret and nowhere to get it from is a wiring mistake, not user input, so it fails
	// loudly rather than looking like a variable someone forgot to define.
	r := newResolution(t, context.Background(), NewChain(Scope{secretVar("apiToken")}), nil)

	_, found, err := r.lookup("apiToken")
	require.False(t, found)
	require.ErrorContains(t, err, `variable "apiToken" is a secret`)
	require.ErrorContains(t, err, "no secret source")
}

func TestResolutionStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	source := &fakeSource{values: map[string]string{"apiToken": "sk-live-abc123"}}
	r := newResolution(t, ctx, NewChain(Scope{secretVar("apiToken")}), source)

	_, found, err := r.lookup("apiToken")
	require.False(t, found)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, source.callCount, "a cancelled run stops asking, whatever the source would have done")
}

func TestResolutionStopsOnACancelledContextForAnyVariable(t *testing.T) {
	// Cancellation is checked on every reference, not only the ones that reach a source: a
	// long expansion is the case a user is most likely to want to abandon, and none of it
	// touches a keychain.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := newResolution(t, ctx, NewChain(Scope{v("baseUrl", "https://api.example.com")}), nil)

	_, found, err := r.lookup("baseUrl")
	require.False(t, found)
	require.ErrorIs(t, err, context.Canceled)
}

func TestResolutionRecordsASecretSetDuringTheRun(t *testing.T) {
	// A token a pre-request script exchanges credentials for has no keychain entry to be
	// fetched from — it arrives through the overlay — but it still has to be masked.
	const token = "sk-live-abc123"
	chain := NewChain()
	chain.SetSecret("apiToken", token)
	r := newResolution(t, context.Background(), chain, nil)

	value, found, err := r.lookup("apiToken")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, token, value, "the overlay holds the value: nothing is fetched")
	require.Equal(t, []string{token}, r.secrets.Values())
}

func TestResolutionDoesNotRecordAnOrdinarySetValue(t *testing.T) {
	// Set is the common case and stays cheap: only SetSecret asks for masking, or every
	// variable a script wrote would be redacted out of its own console output.
	chain := NewChain()
	chain.Set("page", "2")
	r := newResolution(t, context.Background(), chain, nil)

	value, found, err := r.lookup("page")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "2", value)
	require.Empty(t, r.secrets.Values())
}

func TestResolutionHandsTheContextToTheSource(t *testing.T) {
	// A keychain read or a prompt is I/O, so the source has to receive the run's context or
	// it could never be interrupted.
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "run-42")

	source := &fakeSource{values: map[string]string{"apiToken": "sk-live-abc123"}}
	r := newResolution(t, ctx, NewChain(Scope{secretVar("apiToken")}), source)

	_, _, err := r.lookup("apiToken")
	require.NoError(t, err)
	require.Equal(t, "run-42", source.gotCtx.Value(key{}), "the source got the run's own context")
}

func TestResolutionResolvesSecretsThroughTheExpander(t *testing.T) {
	// The two halves together: a reference in a header value becomes the credential, and the
	// same resolution can then mask it on the way to a log.
	const token = "sk-live-abc123"
	source := &fakeSource{values: map[string]string{"apiToken": token}}
	r := newResolution(t, context.Background(), NewChain(
		Scope{secretVar("apiToken"), v("scheme", "Bearer")},
	), source)

	got, err := newExpander(r.lookup).expand("{{scheme}} {{apiToken}}")
	require.NoError(t, err)
	require.Equal(t, "Bearer "+token, got)

	require.Equal(t, "Bearer "+Mask, r.secrets.Redact(got))
}

func TestSecretsRedact(t *testing.T) {
	var secrets Secrets
	secrets.add("sk-live-abc123")
	secrets.add("hunter2")

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "empty text", text: "", want: ""},
		{name: "nothing to mask", text: "GET /v1/users", want: "GET /v1/users"},
		{
			name: "single occurrence",
			text: "Authorization: Bearer sk-live-abc123",
			want: "Authorization: Bearer " + Mask,
		},
		{
			name: "every occurrence",
			text: "sk-live-abc123 then sk-live-abc123",
			want: Mask + " then " + Mask,
		},
		{
			name: "several secrets at once",
			text: `{"token":"sk-live-abc123","password":"hunter2"}`,
			want: `{"token":"` + Mask + `","password":"` + Mask + `"}`,
		},
		{
			name: "inside a longer word",
			text: "prefix-sk-live-abc123-suffix",
			want: "prefix-" + Mask + "-suffix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, secrets.Redact(tt.text))
		})
	}
}

func TestSecretsRedactMasksTheLongestFirst(t *testing.T) {
	// One secret containing another is the case that leaks: masking the short one first would
	// leave the remainder of the long one in the output.
	var secrets Secrets
	secrets.add("abc")          // added first, deliberately
	secrets.add("abc123456789") // and contains the first

	require.Equal(t, Mask+" and "+Mask, secrets.Redact("abc123456789 and abc"))
}

func TestSecretsRedactWithNoSecrets(t *testing.T) {
	var secrets Secrets
	require.Equal(t, "nothing to hide", secrets.Redact("nothing to hide"))
	require.Empty(t, secrets.Values())
}

func TestSecretsIgnoreEmptyAndRepeatedValues(t *testing.T) {
	var secrets Secrets
	secrets.add("") // masking "" would put Mask between every character
	secrets.add("token")
	secrets.add("token") // the same secret used twice is one secret
	secrets.add("other")

	require.Equal(t, []string{"token", "other"}, secrets.Values(), "recorded once, in first-use order")
	require.Equal(t, "abc", secrets.Redact("abc"), "an empty value never became a pattern")
}

func TestSecretsValuesIsACopy(t *testing.T) {
	var secrets Secrets
	secrets.add("token")

	values := secrets.Values()
	values[0] = "tampered"

	require.Equal(t, []string{"token"}, secrets.Values(), "the caller cannot reach into the set")
}

func TestSecretsAddBasic(t *testing.T) {
	// The encoding has to match what the engine puts on the wire — net/http sends
	// base64("user:password") — or the recorded value would mask nothing.
	var secrets Secrets
	secrets.add("l0velace")
	secrets.addBasic("ada", "l0velace")

	const header = "Authorization: Basic YWRhOmwwdmVsYWNl"
	require.Equal(t, "Authorization: Basic "+Mask, secrets.Redact(header),
		"the encoded credential is masked, not just the password it decodes to")
	require.Equal(t, []string{"l0velace", "YWRhOmwwdmVsYWNl"}, secrets.Values())
}

func TestSecretsAddBasicIgnoresAnEmptyPair(t *testing.T) {
	// base64(":") is "Og==" — four characters of ordinary text, not a credential.
	var secrets Secrets
	secrets.addBasic("", "")

	require.Empty(t, secrets.Values())
	require.Equal(t, "Og==", secrets.Redact("Og=="))
}
