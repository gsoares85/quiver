// Package vars resolves the {{variable}} references in a stored request against the scopes
// that surround it, so one collection can run against local, staging, and production
// without a request being edited. It is shared by the request engine and the collection
// runner: if each front-end decided for itself what {{baseUrl}} means, a request would stop
// behaving identically in the app and in CI.
//
// # Reference syntax
//
// A reference is {{name}}, where name is one or more letters, digits, or any of "_.-" —
// with no interior spaces. Anything else that merely looks like a reference is left exactly
// as written, which is what lets the braces that occur naturally in payloads survive: a
// Handlebars template, a CSS block, a JSON object printed inside a string.
//
// A literal {{ is written \{{. The backslash is special only immediately before {{, and
// literal everywhere else, so there is no escaping rule to learn beyond that one sequence.
//
// A value may itself contain references, and those resolve too. A variable that refers back
// to itself, directly or through others, is reported as a *CycleError naming the loop rather
// than recursed until the stack gives out.
//
// A name that no scope defines is left in the text as written and recorded, so a caller can
// report every unresolved reference at once instead of making the user fix them one run at
// a time. It is never quietly replaced with an empty string: sending
// "Authorization: Bearer " is the worst outcome available.
//
// The package depends only on internal/model and the standard library — no storage, no
// scripting, no UI.
package vars
