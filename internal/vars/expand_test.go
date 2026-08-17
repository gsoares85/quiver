package vars

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// mapLookup resolves names from a map, which is all expansion needs to be tested against.
func mapLookup(values map[string]string) lookupFunc {
	return func(name string) (string, bool, error) {
		value, ok := values[name]
		return value, ok, nil
	}
}

// expandWith expands text against a set of values and asserts it succeeded.
func expandWith(t *testing.T, values map[string]string, text string) (string, *expander) {
	t.Helper()
	e := newExpander(mapLookup(values))
	got, err := e.expand(text)
	require.NoError(t, err)
	return got, e
}

func TestExpandSubstitutesReferences(t *testing.T) {
	values := map[string]string{
		"baseUrl":  "https://api.example.com",
		"version":  "v1",
		"empty":    "",
		"greeting": "olá, mundo",
		"api.key":  "dotted",
		"api-key":  "dashed",
		"api_key":  "underscored",
	}

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "whole string", text: "{{baseUrl}}", want: "https://api.example.com"},
		{name: "inside a string", text: "{{baseUrl}}/{{version}}/users", want: "https://api.example.com/v1/users"},
		{name: "adjacent references", text: "{{version}}{{version}}", want: "v1v1"},
		{name: "repeated reference", text: "{{version}} and {{version}}", want: "v1 and v1"},
		{name: "a defined empty value", text: "[{{empty}}]", want: "[]"},
		{name: "non-ascii value", text: "{{greeting}}!", want: "olá, mundo!"},
		{name: "non-ascii around a reference", text: "ação={{version}}", want: "ação=v1"},
		{name: "punctuation in names", text: "{{api.key}} {{api-key}} {{api_key}}", want: "dotted dashed underscored"},
		{name: "no references at all", text: `{"name":"ada"}`, want: `{"name":"ada"}`},
		{name: "empty text", text: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, e := expandWith(t, values, tt.text)
			require.Equal(t, tt.want, got)
			require.Empty(t, e.unresolvedNames())
		})
	}
}

func TestExpandLeavesLookalikesAlone(t *testing.T) {
	// Braces occur naturally in payloads. Anything that is not exactly {{name}} must survive
	// byte for byte, or the resolver would corrupt bodies it was only meant to pass through.
	values := map[string]string{"a": "VALUE", "spaced": "nope"}

	tests := []struct {
		name string
		text string
	}{
		{name: "spaces inside", text: "{{ spaced }}"},
		{name: "leading space", text: "{{ a}}"},
		{name: "trailing space", text: "{{a }}"},
		{name: "empty name", text: "{{}}"},
		{name: "two words", text: "{{a b}}"},
		{name: "expression", text: "{{a + b}}"},
		{name: "unclosed", text: "{{a"},
		{name: "open only", text: "{{"},
		{name: "close only", text: "}}"},
		{name: "single braces", text: "{a}"},
		{name: "one closing brace", text: "{{a}"},
		{name: "json object", text: `{"nested":{"deep":true}}`},
		{name: "css", text: "body { margin: 0 }"},
		{name: "shell", text: "${HOME}/bin"},
		{name: "backslash alone", text: `C:\Users\ada`},
		{name: "backslash before other text", text: `\n\t\x`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, e := expandWith(t, values, tt.text)
			require.Equal(t, tt.text, got, "text that is not a reference must pass through untouched")
			require.Empty(t, e.unresolvedNames())
		})
	}
}

func TestExpandNestedBracesFindTheInnerReference(t *testing.T) {
	// {{{{a}}}} is an outer pair of literal braces around a real reference. Documented here
	// because it is the one lookalike that does contain something to expand.
	got, e := expandWith(t, map[string]string{"a": "VALUE"}, "{{{{a}}}}")
	require.Equal(t, "{{VALUE}}", got)
	require.Empty(t, e.unresolvedNames())
}

func TestExpandEscapesALiteralOpener(t *testing.T) {
	values := map[string]string{"name": "ada", "tpl": `\{{name}}`}

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "escaped reference stays literal", text: `\{{name}}`, want: "{{name}}"},
		{name: "escaped inside a string", text: `hello \{{name}}!`, want: "hello {{name}}!"},
		{name: "escaped and live side by side", text: `\{{name}} is {{name}}`, want: "{{name}} is ada"},
		{name: "handlebars payload", text: `{"template":"Hi \{{firstName}}"}`, want: `{"template":"Hi {{firstName}}"}`},
		{name: "escape at the very end", text: `trailing \{{`, want: "trailing {{"},
		{name: "backslash kept before other braces", text: `\{single}`, want: `\{single}`},
		{name: "double backslash keeps one", text: `\\{{name}}`, want: `\{{name}}`},
		{name: "escape inside a resolved value", text: "{{tpl}}", want: "{{name}}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, e := expandWith(t, values, tt.text)
			require.Equal(t, tt.want, got)
			require.Empty(t, e.unresolvedNames(), "an escaped reference is not an unresolved one")
		})
	}
}

func TestExpandBoundsTotalWorkNotJustDepth(t *testing.T) {
	// Every value names the next one twice: no name repeats, so it is not a cycle, and the
	// chain is far shallower than maxDepth — yet it doubles at every level. Without a budget
	// this allocates gigabytes before it stops. It must fail fast instead.
	const levels = 20 // 2^20 substitutions if nothing stops it, at a depth maxDepth allows
	doubling := func(name string) (string, bool, error) {
		var step int
		if _, err := fmt.Sscanf(name, "v%d", &step); err != nil {
			return "", false, nil
		}
		if step == levels {
			return "leaf", true, nil
		}
		return fmt.Sprintf("{{v%d}}{{v%d}}", step+1, step+1), true, nil
	}

	e := newExpander(doubling)
	_, err := e.expand("{{v0}}")
	require.ErrorContains(t, err, fmt.Sprintf("exceeded %d substitutions", maxExpansions))
	require.Less(t, e.expansions, maxExpansions*2, "the budget must stop the walk, not merely report it afterwards")

	var cycle *CycleError
	require.NotErrorAs(t, err, &cycle, "a doubling chain repeats no name")
}

func TestExpandSpendsTheBudgetAcrossTheWholeRequest(t *testing.T) {
	// One expander spans every field of a request, so the budget is per resolution rather
	// than per string: a thousand fields each just under the limit is the same attack.
	e := newExpander(mapLookup(map[string]string{"a": "x"}))

	text := strings.Repeat("{{a}}", maxExpansions)
	got, err := e.expand(text)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("x", maxExpansions), got)

	_, err = e.expand("{{a}}")
	require.ErrorContains(t, err, fmt.Sprintf("exceeded %d substitutions", maxExpansions))
}

func TestExpandResolvesNestedReferences(t *testing.T) {
	values := map[string]string{
		"url":     "{{scheme}}://{{host}}/{{version}}",
		"scheme":  "https",
		"host":    "{{sub}}.example.com",
		"sub":     "api",
		"version": "v1",
	}

	got, e := expandWith(t, values, "{{url}}/users")
	require.Equal(t, "https://api.example.com/v1/users", got)
	require.Empty(t, e.unresolvedNames())
}

func TestExpandRecordsUnresolvedNames(t *testing.T) {
	values := map[string]string{"known": "yes", "wrapper": "{{missingInside}}"}
	e := newExpander(mapLookup(values))

	// A missing name is left exactly as written, so the text stays diagnosable.
	got, err := e.expand("{{known}}/{{missingA}}/{{missingB}}")
	require.NoError(t, err)
	require.Equal(t, "yes/{{missingA}}/{{missingB}}", got)

	// Nested values are searched too, and the same expander accumulates across calls.
	got, err = e.expand("{{wrapper}}")
	require.NoError(t, err)
	require.Equal(t, "{{missingInside}}", got)

	// Repeats do not pile up, and the order is the order the user would scan.
	got, err = e.expand("{{missingA}} again")
	require.NoError(t, err)
	require.Equal(t, "{{missingA}} again", got)

	require.Equal(t, []string{"missingA", "missingB", "missingInside"}, e.unresolvedNames())
}

func TestExpandDetectsCycles(t *testing.T) {
	tests := []struct {
		name     string
		values   map[string]string
		text     string
		wantPath []string
	}{
		{
			name:     "refers to itself",
			values:   map[string]string{"a": "{{a}}"},
			text:     "{{a}}",
			wantPath: []string{"a", "a"},
		},
		{
			name:     "two variables in a loop",
			values:   map[string]string{"a": "{{b}}", "b": "{{a}}"},
			text:     "{{a}}",
			wantPath: []string{"a", "b", "a"},
		},
		{
			name:     "three variables in a loop",
			values:   map[string]string{"a": "{{b}}", "b": "{{c}}", "c": "{{a}}"},
			text:     "{{a}}",
			wantPath: []string{"a", "b", "c", "a"},
		},
		{
			// The reported path is the loop itself, not the path taken to reach it.
			name:     "loop reached through another variable",
			values:   map[string]string{"entry": "{{a}}", "a": "{{b}}", "b": "{{a}}"},
			text:     "{{entry}}",
			wantPath: []string{"a", "b", "a"},
		},
		{
			name:     "loop inside a longer value",
			values:   map[string]string{"a": "prefix {{b}} suffix", "b": "and {{a}}"},
			text:     "start {{a}}",
			wantPath: []string{"a", "b", "a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newExpander(mapLookup(tt.values))
			_, err := e.expand(tt.text)

			var cycle *CycleError
			require.ErrorAs(t, err, &cycle)
			require.Equal(t, tt.wantPath, cycle.Path)
			require.Equal(t, "variable cycle: "+strings.Join(tt.wantPath, " -> "), cycle.Error())
		})
	}
}

func TestExpandStopsAtTheDepthLimit(t *testing.T) {
	// A chain that never repeats a name defeats the cycle detector, so the depth ceiling is
	// what keeps it from running away.
	deep := func(name string) (string, bool, error) {
		var step int
		if _, err := fmt.Sscanf(name, "v%d", &step); err != nil {
			return "", false, nil
		}
		return fmt.Sprintf("{{v%d}}", step+1), true, nil
	}

	e := newExpander(deep)
	_, err := e.expand("{{v0}}")
	require.ErrorContains(t, err, fmt.Sprintf("nests deeper than %d levels", maxDepth))

	var cycle *CycleError
	require.NotErrorAs(t, err, &cycle, "a long chain is not a cycle")
}

func TestExpandStaysWithinTheDepthLimit(t *testing.T) {
	// One level short of the ceiling must still resolve, or the limit would be rejecting
	// legitimate nesting.
	chain := func(name string) (string, bool, error) {
		var step int
		if _, err := fmt.Sscanf(name, "v%d", &step); err != nil {
			return "", false, nil
		}
		if step == maxDepth-1 {
			return "end", true, nil
		}
		return fmt.Sprintf("{{v%d}}", step+1), true, nil
	}

	e := newExpander(chain)
	got, err := e.expand("{{v0}}")
	require.NoError(t, err)
	require.Equal(t, "end", got)
}

func TestExpandReportsLookupFailures(t *testing.T) {
	// A scope that knows a name but cannot produce its value — a keychain that refused — is
	// a failure, not an unresolved reference.
	const secret = "s3cret-value-do-not-log"
	boom := errors.New("keychain locked")

	e := newExpander(func(name string) (string, bool, error) {
		if name == "token" {
			return secret, true, boom
		}
		return "", false, nil
	})

	_, err := e.expand("Bearer {{token}}")
	require.ErrorIs(t, err, boom)
	require.ErrorContains(t, err, `resolve variable "token"`)
	require.NotContains(t, err.Error(), secret, "a failure must never carry the value it could not use")
	require.Empty(t, e.unresolvedNames(), "a failed lookup is not an unresolved name")
}

func TestIsRefName(t *testing.T) {
	// parseRef never hands it an empty name, but a predicate that answered "yes" to one
	// would be wrong on its own terms.
	require.False(t, isRefName(""))
	require.True(t, isRefName("baseUrl"))
	require.True(t, isRefName("api.key-2_x"))
	require.False(t, isRefName("has space"))
	require.False(t, isRefName("emoji🎯"))
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		text      string
		wantName  string
		wantWidth int
		wantOK    bool
	}{
		{text: "{{a}}", wantName: "a", wantWidth: 5, wantOK: true},
		{text: "{{baseUrl}}/users", wantName: "baseUrl", wantWidth: 11, wantOK: true},
		{text: "{{a}}{{b}}", wantName: "a", wantWidth: 5, wantOK: true},
		{text: "{{a b}}", wantOK: false},
		{text: "{{}}", wantOK: false},
		{text: "{{a", wantOK: false},
		{text: "not a reference", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			name, width, ok := parseRef(tt.text)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantName, name)
			require.Equal(t, tt.wantWidth, width)
		})
	}
}
