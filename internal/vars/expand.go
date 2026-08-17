package vars

import (
	"fmt"
	"slices"
	"strings"
)

const (
	// refOpen and refClose delimit a variable reference.
	refOpen  = "{{"
	refClose = "}}"

	// escapedOpen is how text that must keep a literal {{ writes it. Two backslashes, not
	// one: a single backslash stays an ordinary character, so a Windows path may sit
	// directly in front of a reference — C:\{{dir}}\file.json — and still resolve.
	escapedOpen = `\\` + refOpen

	// maxDepth bounds how far a value may nest further references. Cycles are caught by
	// name, so this only guards a chain that keeps resolving to something new: far above
	// any real workspace, far below a stack overflow.
	maxDepth = 32

	// maxExpansions bounds how many references one resolution may substitute. Depth alone
	// does not bound the work: a value that names the next variable twice doubles at every
	// level, so a chain that never repeats a name and stays well inside maxDepth still
	// expands to gigabytes. Far above any real request, far below anything that hurts.
	maxExpansions = 10_000
)

// lookupFunc supplies the value of a variable name. found is false when no scope defines
// the name, which is what keeps an unresolved reference visible instead of blank. The error
// return exists because a scope can know a name and still fail to produce its value — a
// secret whose keychain refused, for instance.
type lookupFunc func(name string) (value string, found bool, err error)

// CycleError reports a variable that refers back to itself, directly or through others.
type CycleError struct {
	Path []string // the loop, with the repeated name at both ends
}

func (e *CycleError) Error() string {
	return "variable cycle: " + strings.Join(e.Path, " -> ")
}

// expander substitutes variable references and remembers what it could not resolve. One
// expander spans every string in a request, so a caller can report all of the unresolved
// names together rather than surfacing them one run at a time.
type expander struct {
	look       lookupFunc
	unresolved []string
	seen       map[string]bool
	expansions int // substitutions performed so far, against maxExpansions
}

func newExpander(look lookupFunc) *expander {
	return &expander{look: look, seen: map[string]bool{}}
}

// expand replaces every well-formed reference in text with its value, expanding the
// references that appear inside those values too. See the package doc for the syntax.
func (e *expander) expand(text string) (string, error) {
	return e.substitute(text, nil, 0)
}

// unresolvedNames lists the names no scope defined, in the order they were first seen.
func (e *expander) unresolvedNames() []string { return e.unresolved }

// substitute walks text once. active is the stack of names currently being expanded, which
// serves as both the cycle detector and the path reported when a cycle is found.
func (e *expander) substitute(text string, active []string, depth int) (string, error) {
	var b strings.Builder
	for i := 0; i < len(text); {
		rest := text[i:]

		// Copy everything up to the next byte that could begin a reference or an escape.
		// Bodies run to megabytes, and most of those bytes are not braces.
		next := strings.IndexAny(rest, `{\`)
		if next < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:next])
		i += next
		rest = rest[next:]

		if strings.HasPrefix(rest, escapedOpen) {
			b.WriteString(refOpen) // both backslashes are consumed; the braces stay
			i += len(escapedOpen)
			continue
		}

		name, width, ok := parseRef(rest)
		if !ok {
			b.WriteByte(text[i]) // not a reference: an ordinary brace or backslash
			i++
			continue
		}
		i += width

		value, found, err := e.look(name)
		if err != nil {
			return "", fmt.Errorf("resolve variable %q: %w", name, err)
		}
		if !found {
			e.markUnresolved(name)
			b.WriteString(refOpen + name + refClose) // left visible, not blanked
			continue
		}

		// Charge the budget here, where a reference actually turns into text. Depth and the
		// cycle detector bound the shape of the expansion; only a count bounds its size.
		e.expansions++
		if e.expansions > maxExpansions {
			return "", fmt.Errorf(
				"variable expansion exceeded %d substitutions, which usually means a value refers to itself indirectly",
				maxExpansions,
			)
		}

		if !strings.Contains(value, refOpen) {
			b.WriteString(value) // nothing nested, nothing to recurse into
			continue
		}
		if at := slices.Index(active, name); at >= 0 {
			return "", &CycleError{Path: append(slices.Clone(active[at:]), name)}
		}
		if depth >= maxDepth {
			return "", fmt.Errorf("variable %q nests deeper than %d levels", name, maxDepth)
		}
		nested, err := e.substitute(value, append(active, name), depth+1)
		if err != nil {
			return "", err
		}
		b.WriteString(nested)
	}
	return b.String(), nil
}

// markUnresolved records a name once, keeping the order names were first met so the report
// reads in the order the user would scan their request.
func (e *expander) markUnresolved(name string) {
	if e.seen[name] {
		return
	}
	e.seen[name] = true
	e.unresolved = append(e.unresolved, name)
}

// parseRef reads a reference at the start of text and reports the name with the number of
// bytes it occupies. It fails on anything that is not exactly {{name}} — a space, an empty
// name, a missing close — which is what keeps ordinary braces from being mistaken for
// variables.
func parseRef(text string) (name string, width int, ok bool) {
	if !strings.HasPrefix(text, refOpen) {
		return "", 0, false
	}
	rest := text[len(refOpen):]
	end := strings.Index(rest, refClose)
	if end <= 0 { // no close, or nothing between the braces
		return "", 0, false
	}
	name = rest[:end]
	if !isRefName(name) {
		return "", 0, false
	}
	return name, len(refOpen) + end + len(refClose), true
}

// isRefName reports whether name is a variable name: letters, digits, and any of "_.-".
// Deliberately narrow — a reference names a variable, it does not hold an expression.
func isRefName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}
