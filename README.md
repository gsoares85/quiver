<div align="center">

# Quiver

**The open source API client that lives in your git repo, runs in your terminal,
and never holds your work hostage.**

*A local-first, git-native alternative to Postman.*

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

Early development. The local-first single-user core comes first; collaboration
and extensibility land in later phases.

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

## Quick start (development)

> Scaffolding is in progress; these are the intended commands.

```bash
# Prerequisites: Go, Node.js + pnpm, and the Wails CLI.

# Run the desktop app with hot reload
wails dev

# Run the CLI against a collection
go run ./cmd/quiver run collections/users-api --env local

# Run tests
go test ./...
```

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
