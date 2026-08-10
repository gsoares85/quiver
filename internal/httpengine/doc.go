// Package httpengine executes resolved requests against real endpoints — HTTP today,
// WebSocket, gRPC, and GraphQL as they arrive — with streaming responses, accurate
// timing, auth, and full context-based cancellation. It has no knowledge of storage,
// scripting, or the UI, so the desktop app and the CLI reach the network through exactly
// the same code and a request runs identically in both.
//
// # The streaming contract
//
// Execute returns a channel carrying, in order: one Meta chunk once the status and
// headers are known, zero or more Body chunks as the payload arrives, and exactly one
// terminal chunk — Done, with the assembled response, its timing, and a Trace, or Err.
// The channel is closed once the terminal chunk has been delivered.
//
// The caller must drain the channel or cancel the context. A stream that is abandoned
// without either leaves the engine holding a chunk nobody will take.
//
// Failures arrive in one of two ways. A request that could not be attempted at all — an
// unknown method, an unusable URL, unflattened auth, an unreadable body file — fails
// synchronously, with a nil channel and an error. Anything that goes wrong once the
// request is on the wire arrives as the terminal Err chunk. Both carry an *Error whose
// Kind classifies the failure for a front-end to render or a CLI to turn into an exit
// code.
//
// # What the caller must do first
//
// A request handed to Execute is fully resolved: variables substituted, auth inheritance
// flattened, workspace-level settings folded into the request's own Settings, and
// disabled entries dropped. Secret values are resolved by the caller too — the engine
// never reads the OS keychain, and never writes a credential into an error message.
package httpengine
