# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Amagi CodeBox — a Wails v2 desktop app (Go backend + Vue 3/TS frontend, compiled into one binary) that manages configurations for five AI-CLI apps: **Claude Code**, **OpenCode**, **Codex**, **Pi**, and **Oh My Pi (omp)**. It manages multiple service providers/presets, stores API keys via OS keychain, launches and proxies CLI sessions with an embedded terminal (xterm.js + ConPTY/PTY), and exposes a remote-control HTTP/WebSocket API for a companion mobile app. Targets Windows 10+ and macOS.

## Common commands

### Build & dev (Wails wraps frontend build automatically)
```bash
wails dev                 # hot-reload dev mode (frontend + Go)
wails build               # production build → build/bin/
./build.sh                # macOS/Linux one-shot build (= frontend build + mobile build + wails build)
build.bat                 # Windows equivalent
```

### Frontend only (run from repo root or `frontend/`)
```bash
npm --prefix frontend run dev     # vite dev server only
npm --prefix frontend run build   # = vue-tsc --noEmit && vite build (typecheck gates build)
npm --prefix frontend install
npm run build:mobile              # build the separate Capacitor mobile frontend (mobile/)
npm run build                    # mobile + frontend
```

### Go lint & test
```bash
go vet ./...                                   # what CI actually runs
go test ./...                                  # CI runs this on macos-latest; run manually for full local coverage
go test ./internal/config -run TestServiceName # single package / single test
go test -race ./internal/session               # with race detector (concurrency-heavy packages)
```
Note: `.github/workflows/ci.yml` runs `go vet ./...` (windows + macos), full `go test ./... -count=1` on macos-latest (windows-latest only compile-checks via `-run '^$'`), plus frontend/mobile builds and the mobile Vitest suite. The `envcheck.test` file in the repo root is a stale local test binary that was never committed to git, not a source file — ignore it.

### Toolchain version baseline (C-001)
**CI/release** precisely pins **Go `1.25.0`** and **Node `20.19.0`** (exact versions in `ci.yml`/`release.yml` setup-go/setup-node). **`go.mod`** declares the project's Go language baseline (`go 1.25.0`) — this is the minimum toolchain the module targets, not a local version lock: there is no `toolchain` directive and no `.go-version`, so a locally installed newer Go (e.g. 1.26.x) is used as-is without forced downgrade. **Node** is locally pinned by the root `.node-version` file (`20.19.0`, consumed by fnm/nodenv). npm stays `>=10` in the manifests (not engine-strict).

### Version injection
`main.Version/BuildTime/GitCommit/GoVersion` are injected at build time via ldflags. Source of truth: `build.sh`/`build.bat` read `git describe --tags`, falling back to `wails.json` `info.productVersion`, then `dev`. Bump version by editing `wails.json` (and the two `package.json` files).

## Architecture

### The binding spine (read multiple files together)
`main.go` boots Wails and **binds** the `App` struct plus 16 service structs (17 bindings total) to the frontend — the exact list lives in `bind_list.go` (`buildWailsBindList`). `app.go` (~214KB) is the central hub: it holds pointers to every service and exposes orchestration methods (session launch, env-check actions, remote-control toggles, callback registration). Each bound struct's exported methods become callable from JS. The full surface is documented in `docs/api.md`.

Service packages live under `internal/`. Each follows the same pattern: a `Service`/`ConfigService` struct with a `New...()` constructor and exported methods, e.g. `internal/config` (`ConfigService` → providers/presets), `internal/secrets` (key storage), `internal/session` (app sessions), `internal/plugin` + `internal/codexplugin`, `internal/envcheck`, `internal/remote`, `internal/pty`, `internal/updater`, and `internal/headroom`.

### Cross-platform via build tags (do not branch at runtime)
Platform differences are handled by Go build constraints, **not** runtime `if runtime.GOOS`. Files are suffixed `_<os>.go`:
- Secrets: `store_windows.go` (DPAPI) / `store_darwin_cgo.go` & `store_darwin_nocgo.go` (Keychain) / `store_other.go` (**unsupported no-op** — on Linux/other there is no keychain: `Load` returns an empty map, `Save` silently drops; there is intentionally no plaintext fallback).
- PTY: `service_darwin.go` (creack/pty) / `service_other_stub.go`. ConPTY (Windows) lives in the UserExistsError/conpty dep.
- `internal/platform/`: capabilities, file opener, single-instance mutex, process policy, shell catalog — each split per OS.
- `tray_icon_windows.go` / `tray_icon_nonwindows.go` at repo root.

When editing platform-specific behavior, edit the correct `_<os>.go` file for your target and keep the stubs consistent. Capabilities are resolved once at startup via `platform.CurrentCapabilities()`.

### Frontend ↔ backend bridge
Wails auto-generates TypeScript bindings under `frontend/wailsjs/go/<pkg>/` from the bound Go methods whenever you run `wails dev`/`wails build`. **Never hand-edit `frontend/wailsjs/`** — regenerate it. `frontend/src/api/*.ts` modules wrap those raw bindings into typed, ergonomic functions (one per domain: `provider.ts`, `session.ts`, `plugin.ts`, etc.), and Pinia stores in `frontend/src/stores/` consume them. Flow: Vue view → composable/store → `src/api/*.ts` → `wailsjs/go/...` → Go service.

Routing uses hash history (`createWebHashHistory`) in `frontend/src/router/index.ts`. UI is built on the app's own `frontend/src/components/ui/` component kit + a custom design-token layer in `frontend/src/styles/tokens.css` (Element Plus was fully removed in the 2025-08 bundle-slimming pass — do not reintroduce it).

### Managed app types & sessions
Five CLI app types defined in `internal/session/types.go` as `AppType`: `claudecode`, `opencode`, `codex`, `pi`, `omp` (Oh My Pi). `LaunchSession` in `app.go` is the core entrypoint for Claude Code — it resolves provider/preset, optionally enables Headroom, then spawns a PTY session tracked by the session manager. Pi/omp sessions launch through their own entrypoints (`LaunchPiSession`/`LaunchOmpSession`), which write provider configs into the CLI's own agent root (`~/.pi/agent` / `~/.omp/agent`). Sessions stream output to the frontend via registered callbacks (`RegisterOutputCallback`, etc.).

### Remote control & mobile
`internal/remote/` runs an HTTP + WebSocket server (when enabled) for the companion Capacitor app in `mobile/`. Endpoints documented in README; all require an `Authorization` token. The mobile frontend is a **separate build** (`mobile/`) embedded via `//go:embed all:mobile/dist` in `main.go` — it is not the desktop frontend.

## Conventions

- **Config lives in `~/.amagi-codebox/`**: `models.json` (providers/presets), `secrets.enc` (platform-protected keys), `settings.json`, `paths.json`, `envvars.json`, pricing, and remote security state. The app reads/writes these via the service layer; don't parse them ad hoc. Fresh installs intentionally contain no provider or terminal-preset seeds.
- **JSON edits**: this repo uses `tidwall/gjson` + `tidwall/sjson` for surgical JSON mutation (config files, manifests) rather than unmarshal-mutate-marshal in many places. Match that style for partial edits.
- **Code & docs are bilingual** (Chinese + English) — follow the surrounding file's language.
- **Amagi runtime artifacts**, not app code: `agent-outputs/`, `.amagi/`, `projects-memory/`. The `.amagi-codebox/frontend-redesign` handoff doc in `demo/` describes a prior frontend rework.
- Vendored deps are committed (`vendor/`); builds use `-mod=vendor` semantics — add new Go deps via `go get` then `go mod vendor`.
