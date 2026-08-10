<div align="center">

# Quiver

**The open source API client that lives in your git repo, runs in your terminal,
and never holds your work hostage.**

*A local-first, git-native alternative to Postman.*

[![CI](https://github.com/gsoares85/quiver/actions/workflows/ci.yml/badge.svg)](https://github.com/gsoares85/quiver/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/gsoares85/quiver?include_prereleases&sort=semver&display_name=tag)](https://github.com/gsoares85/quiver/releases)

> ⚠️ **Working codename.** "Quiver" is a placeholder name pending finalization.

</div>

---

## Why Quiver

Postman is powerful, but it locks your collections in the cloud, isn't naturally
version-controllable, and ships as a heavyweight app. Quiver takes a different
stance:

- 📁 **Local-first & git-native.** Collections are plain-text files in *your*
  repo. Real `git diff`, code review, branches — no lock-in.
- ⚡ **Lightweight & fast.** A native desktop app (Go + Wails), not a bundled
  browser. Small binary, low memory.
- 🧪 **Requests as executable specs.** First-class test authoring with a familiar
  JavaScript API and assertions.
- 🤖 **CLI + CI runner.** The same requests run in your terminal and your
  pipeline — one engine, identical results.
- 📚 **API documentation.** Generate readable docs (HTML/Markdown/OpenAPI) from
  your collections, kept honest by real saved responses.
- 👥 **Real-time collaboration (opt-in).** Live co-editing via CRDTs that
  materializes back into ordinary, reviewable git diffs.
- 🔒 **Yours.** Fully offline-capable, no mandatory account, secrets never written
  to files. The optional sync relay is self-hostable.

## Status

Early development. **Phase 0 (foundations) is in place:** the Go module and package
skeleton, the `quiver` CLI, the Wails desktop shell, the shared UI package, and a CI
pipeline that enforces linting, tests, a coverage gate, and cross-platform builds.
The shared **domain model** (`internal/model`) defines the core entities — workspaces,
collections, folders, requests, environments, auth, and bodies — with ULID identities
and validation; the **git-native storage engine** (`internal/store`) round-trips those
entities to and from byte-stable `.qv.yaml` files on disk; and the **request engine**
(`internal/httpengine`) sends them, streaming responses back with a per-phase timing
waterfall, authentication, redirect control, and full cancellation. Next up:
`{{variable}}` resolution across environments, and the app and CLI surfaces that put the
engine in front of you.

## Architecture at a glance

| Layer | Technology |
|-------|-----------|
| Core engine | **Go** (`internal/*`) — shared by the app and the CLI |
| Request execution | Go **`net/http`** — HTTP/1.1 + HTTP/2, streaming, `httptrace` timing |
| Desktop shell | **Wails v2** (native webview + Go) |
| Frontend | **React 18 + TypeScript + Vite** |
| CLI | **cobra** (`cmd/quiver`) |
| Storage | Plain-text **`.qv.yaml`** files, git-native |
| Scripting | **goja** (sandboxed JS) |
| Collaboration | **Yjs** (CRDT) + self-hostable Go relay |
| Plugins | **wazero** (sandboxed WASM) |

## Repository layout

```text
.
├─ apps/desktop/        # Wails v2 desktop app
│  ├─ main.go, app.go   # bound structs (thin — delegate to internal/*)
│  ├─ wails.json
│  └─ frontend/         # React 18 + TypeScript + Vite
├─ cmd/quiver/          # `quiver` CLI (cobra)
├─ internal/            # shared engine: model, store, httpengine, script, sync, runner
│  └─ buildinfo/        # version & build metadata
├─ packages/ui/         # shared React design system (@quiver/ui)
├─ .github/workflows/   # CI
├─ go.mod               # module github.com/gsoares85/quiver
└─ README.md
```

The desktop app and the CLI import the same `internal/*` packages, so a request runs
identically in the app and in CI. No execution logic lives in the UI or the shell.

## Storage format

A workspace is plain text on disk, so it version-controls, diffs, reviews, and merges
like source. The collection tree maps directly onto the filesystem — directories are
collections and folders, files are requests:

```text
my-api/                       # workspace root (usually a git repo)
├─ quiver.yaml                # workspace manifest (id, name, settings)
├─ environments/
│  └─ staging.qv.yaml         # a named set of variables
└─ collections/
   └─ users-api/              # a collection (directory)
      ├─ collection.qv.yaml   # collection metadata + child order
      └─ users/               # a folder (directory)
         ├─ folder.qv.yaml    # folder metadata + child order
         └─ get-user.qv.yaml  # a request
```

- **Byte-stable & diff-friendly.** Files use a fixed field order, 2-space indent, and
  `\n` endings, so a one-line change is a one-line `git diff`. A canonical workspace —
  one Quiver wrote itself, already at the current `schemaVersion` — loads and saves back
  byte-for-byte identically. Hand-written files and older workspaces are normalized into
  that canonical form on the first save: migrations upgrade them on load, and every file
  is written back at the current `schemaVersion`.
- **Human-readable & tool-agnostic.** Plain YAML (`*.qv.yaml`) — you, or any tool, can
  create and edit requests by hand.
- **Stable identity & order.** Every entity has a ULID `id`, so renames and merges
  stay clean; a folder records the authored order of its children in an `order:` list
  (absent order sorts alphabetically).
- **Secrets stay out of the files.** A secret variable is stored as a reference
  (`secret: true`, no value); resolving it from the OS keychain is an upcoming feature.
- **Versioned & migratable.** Every file carries a `schemaVersion`; registered
  migrations run on load as the format evolves, so older workspaces keep working.
- **Safe writes.** Each file is written to a temporary file, fsynced, and renamed over
  its target, so no reader ever sees a half-written file. Replacement is per file, not
  per workspace: a save is not a single transaction, so an interrupted one can leave
  some files updated and others not. Machine-local state lives in a git-ignored
  `.quiver/` directory.

## Request engine

Sending a request is implemented exactly once, in `internal/httpengine` — the package the
desktop app and the CLI both run on, so a request behaves identically in the app, in your
terminal, and in CI. Execution logic lives there and nowhere else: not in the UI, not in
the desktop shell.

- **Streaming responses.** A response arrives as a stream: headers first, then body
  chunks as they land, then the assembled result with its timing. Large payloads render
  progressively instead of blocking on one big reply, and the same shape will carry
  long-lived streams (SSE, WebSocket) when they land.
- **HTTP/1.1 and HTTP/2**, negotiated automatically over pooled keep-alive connections.
- **A real timing waterfall.** DNS, connect, TLS handshake, time to first byte, and
  download are measured separately. A zero phase means it genuinely did not happen — a
  reused connection performs no handshake, an IP literal needs no lookup — rather than a
  measurement that went missing.
- **Cancellation and timeouts that reach the body.** Both are enforced through the
  request's context, so they cover a response while it streams rather than only the
  initial reply. A cancelled request stops promptly and still tells you why it ended.
- **Redirects under your control.** They are off by default, so a 3xx *is* the response,
  `Location` header intact and inspectable. Turn them on and every hop is recorded with
  its status and both URLs, while `maxRedirects` acts as a real budget.
- **What a redirect does to your credentials**, since it is rarely what people assume:
  an `Authorization` header — Basic, Bearer, or an OAuth 2.0 token — is dropped as soon as
  a redirect leaves the original host, and kept when it stays. An API key placed in a
  **custom header** is an ordinary header and travels with the request wherever it goes,
  including to another host, so only enable redirect following for hosts you would hand
  that key to directly. An API key placed in the **query string** does not survive a
  redirect at all, because the next request's URL comes from the `Location` header — a
  request that follows one arrives without it.
- **Authentication** applied at send time: Basic, Bearer, and API key (in a header or the
  query string), plus OAuth 2.0 requests that carry an already-acquired token — the grant
  flows themselves come later. Secret values are resolved at run time and never written
  to a file, a log, or an error message.
- **Every body type:** JSON, text, XML, GraphQL, url-encoded forms, multipart uploads,
  and raw binary. Uploads stream from disk instead of being buffered in memory, and a
  followed redirect replays the payload intact.
- **Actionable failures.** Transport errors are classified into a stable set — `dns`,
  `connection_refused`, `tls`, `timeout`, `canceled`, `too_many_redirects` — so the app
  can say something useful and the CLI can turn them into meaningful exit codes.
- **Proxies and TLS.** `HTTP(S)_PROXY` and `NO_PROXY` are honored by default, and
  certificate verification is always on.
- **Requests say who they are.** They go out as `quiver/<version>`, so an operator reading
  their access logs can tell what called them; setting your own `User-Agent` header
  overrides it.

## Development

**Prerequisites:** Go 1.25+, Node 24+, pnpm 11+, and the
[Wails CLI](https://wails.io/) (`wails doctor` verifies your environment).

### Desktop app

```bash
cd apps/desktop
wails dev      # run with hot reload (Go backend + React frontend)
wails build    # build a distributable app into build/bin/
```

The `build/bin/` artifact is platform-specific: `quiver.app` on macOS, `quiver.exe`
on Windows, and `quiver` on Linux.

### CLI

```bash
go run ./cmd/quiver version
```

### Go — build, test, lint

```bash
go build ./internal/... ./cmd/...
go test -cover ./internal/... ./cmd/...
golangci-lint run ./internal/... ./cmd/...
```

> The desktop package (`apps/desktop`) embeds the built frontend and links the OS
> webview, so build it with `wails build` rather than a bare `go build ./...`.

### Frontend (from the repository root)

```bash
pnpm install
pnpm lint            # ESLint across the workspace
pnpm format:check    # Prettier
pnpm -r typecheck    # tsc --noEmit
pnpm -r build        # build @quiver/ui and the desktop frontend
```

## Quality gates & CI

CI runs on every pull request and on pushes to `main`:

- **Go** — `golangci-lint` (gofumpt, govet, staticcheck, errcheck) and `go test`.
- **Frontend** — ESLint, Prettier, `tsc`, and the Vite build.
- **Cross-platform build** — `go build` on macOS, Windows, and Linux.
- **Coverage gate** — total Go coverage (over `./internal/...` and `./cmd/...`) must
  stay **≥ 80%**; a change that drops below fails the build.

## Versioning & releases

Quiver follows **[Semantic Versioning](https://semver.org/)** and ships via
**GitHub Releases** (tagged `vMAJOR.MINOR.PATCH`). `quiver version` reports the
release the binary was built from.

## Contributing

**The project is not accepting external contributions yet.** It is in early
development and the internals are still moving quickly.

Once it reaches its first public release, this section will define how to
contribute — how to set up the project, coding standards, the branch/PR workflow,
and the review process — alongside a `LICENSE`, `CONTRIBUTING.md`, `SECURITY.md`,
and `CODE_OF_CONDUCT.md`.

## License

To be finalized before the first public release (a permissive OSI-approved license
such as Apache-2.0 or MIT is the intended direction).

---

<div align="center">
Built and maintained by <strong>Guilherme Soares</strong>.
</div>
