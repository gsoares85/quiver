<div align="center">

# Quiver

**The open source API client that lives in your git repo, runs in your terminal,
and never holds your work hostage.**

*A local-first, git-native alternative to Postman.*

[![CI](https://github.com/gsoares85/quiver/actions/workflows/ci.yml/badge.svg)](https://github.com/gsoares85/quiver/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/gsoares85/quiver?sort=semver&display_name=tag)](https://github.com/gsoares85/quiver/releases)

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
The local-first single-user core (request engine, storage, and the request/response
editor) comes next.

## Architecture at a glance

| Layer | Technology |
|-------|-----------|
| Core engine | **Go** (`internal/*`) — shared by the app and the CLI |
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

## Development

**Prerequisites:** Go 1.25+, Node 24+, pnpm 11+, and the
[Wails CLI](https://wails.io/) (`wails doctor` verifies your environment).

### Desktop app

```bash
cd apps/desktop
wails dev      # run with hot reload (Go backend + React frontend)
wails build    # build a distributable app → build/bin/quiver.app
```

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

Every push and pull request runs CI:

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
