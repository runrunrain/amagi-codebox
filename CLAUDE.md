# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Amagi CodeBox — a Wails v2 desktop app (Go backend + Vue 3/TS frontend, compiled into one binary) that manages configurations for five AI-CLI apps: **Claude Code**, **OpenCode**, **Codex**, **Pi**, and **Oh My Pi (omp)**. It manages multiple service providers/presets, stores API keys via OS keychain, launches and proxies CLI sessions with an embedded terminal (xterm.js + ConPTY/PTY), runs local usage accounting (SQLite), offers AI-assisted git commit/push (GitAssist), and exposes a remote-control HTTP/WebSocket API for a companion mobile app — plus a Remote Client mode that connects outward to other CodeBox instances. Targets Windows 10+ and macOS.

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
**CI/release** precisely pins **Go `1.25.0`** and **Node `20.19.0`** (exact versions in `ci.yml`/`release.yml` setup-go/setup-node). **`go.mod`** declares the project's Go language baseline (`go 1.25.0`) — this is the minimum toolchain the module targets, not a local version lock: there is no `toolchain` directive and no `.go-version`, so a locally installed newer Go (e.g. 1.26.x) is used as-is without forced downgrade. **Node** is locally pinned by the root `.node-version` file (currently `22.23.2`, consumed by fnm/nodenv — note this intentionally diverges from the CI pin). npm stays `>=10` in the manifests (not engine-strict).

### Version injection
`main.Version/BuildTime/GitCommit/GoVersion` are injected at build time via ldflags. Source of truth: `build.sh`/`build.bat` read `git describe --tags`, falling back to `wails.json` `info.productVersion`, then `dev`. Bump version by editing `wails.json` (and the two `package.json` files).

## Architecture

### The binding spine (read multiple files together)
`main.go` boots Wails and **binds** the `App` struct plus 20 service structs (21 bindings total) to the frontend — the exact list lives in `bind_list.go` (`buildWailsBindList`): Config, Secrets, Paths, Log, Settings, Updater, Plugins, CodexPlugins, OpenCodePlugins, PiPlugins, OmpPlugins, OpenCodeConfig, PiConfig, OmpConfig, AgentProfiles, EnvCheck, Usage, WebUI, Skins, GitAssist. The raw `pty.Service` and `headroom.HeadroomService` are deliberately **excluded** from Bind: the frontend reaches them only through gated `App` facade methods (`PtyWrite`/`PtyResize`, `Headroom*` in `headroom_facade.go`, lease-guarded; control-runtime glue in `control_wiring.go`). `app.go` (~5.8k lines) is the central hub: it holds pointers to every service and exposes orchestration methods (session launch, env-check actions, remote-control toggles, callback registration). Each bound struct's exported methods become callable from JS. The full surface is documented in `docs/api.md`.

Service packages live under `internal/` — 34 top-level packages (39 Go packages counting subpackages like `remote/contract` and `appmeta/*`). Each follows the same pattern: a `Service`/`ConfigService` struct with a `New...()` constructor and exported methods. Core: `internal/config` (providers/presets), `internal/secrets` (key storage), `internal/session` (app sessions), `internal/launcher` + `internal/launchplan` (per-CLI launch & config writing), `internal/plugin` + `internal/codexplugin` + `internal/opencodeplugin` + `internal/piplugin` + `internal/ompplugin` (five plugin engines), `internal/opencodeconfig`/`piconfig`/`ompconfig` (per-CLI config), `internal/envcheck`, `internal/remote` + `internal/remote/contract` (remote server + v1 wire contract), `internal/pty`, `internal/updater`, `internal/headroom`. Newer additions: `internal/gitassist` (AI-assisted git commit/push), `internal/usage` (SQLite usage accounting + pricing), `internal/agentprofile` (agent env profiles), `internal/skins` (image skin library + `/skins/` asset handler), `internal/webui` (pi Web UI shell integration), `internal/wslsetup` (install CLIs inside WSL, Windows-only), `internal/remoteclient` (outbound client to other CodeBox hosts), `internal/structured`, `internal/processcap`, `internal/tray`, `internal/appmeta` (per-CLI metadata queries).

### Cross-platform via build tags (do not branch at runtime)
Platform differences are handled by Go build constraints, **not** runtime `if runtime.GOOS`. Files are suffixed `_<os>.go`:
- Secrets: `store_windows.go` (DPAPI) / `store_darwin_cgo.go` & `store_darwin_nocgo.go` (Keychain) / `store_other.go` (**unsupported no-op** — on Linux/other there is no keychain: `Load` returns an empty map, `Save` silently drops; there is intentionally no plaintext fallback).
- PTY: `service_darwin.go` (creack/pty) / `service_other_stub.go`. ConPTY (Windows) lives in the UserExistsError/conpty dep.
- `internal/platform/`: capabilities, file opener, single-instance mutex, process policy, shell catalog — each split per OS.
- `tray_icon_windows.go` / `tray_icon_nonwindows.go` at repo root.

When editing platform-specific behavior, edit the correct `_<os>.go` file for your target and keep the stubs consistent. Capabilities are resolved once at startup via `platform.CurrentCapabilities()`.

### Frontend ↔ backend bridge
Wails auto-generates TypeScript bindings under `frontend/wailsjs/go/<pkg>/` from the bound Go methods whenever you run `wails dev`/`wails build`. **Never hand-edit `frontend/wailsjs/`** — regenerate it. `frontend/src/api/*.ts` modules wrap those raw bindings into typed, ergonomic functions (one per domain: `provider.ts`, `session.ts`, `plugin.ts`, etc.), and Pinia stores in `frontend/src/stores/` consume them. Flow: Vue view → composable/store → `src/api/*.ts` → `wailsjs/go/...` → Go service.

Routing uses hash history (`createWebHashHistory`) in `frontend/src/router/index.ts`. UI is built on the app's own `frontend/src/components/ui/` component kit + a custom design-token layer in `frontend/src/styles/tokens.css` (Element Plus was fully removed in the 2025-08 bundle-slimming pass — do not reintroduce it). `frontend/src/api/` holds ~26 typed wrapper modules (one per domain: `provider.ts`, `session.ts`, `plugin.ts`, `gitassist.ts`, `usage.ts`, `remoteClient.ts`, `wslsetup.ts`, etc.), consumed by 10 Pinia stores in `frontend/src/stores/`; views include a `settings/` sub-tree plus `UsageView.vue` and `RemoteSessionsView.vue`.

### Managed app types & sessions
Five CLI app types defined in `internal/session/types.go` as `AppType`: `claudecode`, `opencode`, `codex`, `pi`, `omp` (Oh My Pi). `LaunchSession` in `app.go` is the core entrypoint for Claude Code — it resolves provider/preset, optionally enables Headroom, then spawns a PTY session tracked by the session manager. Pi/omp sessions launch through their own entrypoints (`LaunchPiSession`/`LaunchOmpSession`), which write provider configs into the CLI's own agent root (`~/.pi/agent` / `~/.omp/agent`) via `internal/launcher` (`pi_config.go`, `omp_config.go`). Sessions stream output to the frontend via registered callbacks (`RegisterOutputCallback`, etc.).

### Remote control & mobile
`internal/remote/` runs an HTTP + WebSocket server (when enabled) for the companion Capacitor app in `mobile/`. Two independent API surfaces share the server: the legacy `/api/*` + `/ws/terminal/*` Bearer-token API (routes in `internal/remote/handlers.go`) and the v1 contract API (`/api/remote/v1/*` + `/ws/v1`, device-Cookie auth, routes generated from `internal/remote/contract` symbols in `routes_v1.go`; normative doc `docs/developer/remote-api-v1-contract.md`). All requests require authorization; legacy write/sensitive routes additionally require a loopback peer. `internal/remoteclient/` implements the reverse direction — the desktop acting as a client of another CodeBox instance (drives `RemoteSessionsView.vue`). The mobile frontend is a **separate build** (`mobile/`) embedded via `//go:embed all:mobile/dist` in `main.go` — it is not the desktop frontend.

## Conventions

- **Config lives in `~/.amagi-codebox/`**: `models.json` (providers/presets, terminal presets may carry vision/video capability flags — exported to `~/.agents/amagi-media-models.json` per `docs/vision-export-contract.md`), `secrets.enc` (platform-protected keys), `settings.json`, `paths.json`, `envvars.json`, `agent-profiles.json`, `devices.json` (v1 pairing device snapshots), `usage.db` (SQLite usage accounting) + `usage-pricing.json`, plus `skins/` and `logs/` directories. The app reads/writes these via the service layer; don't parse them ad hoc. Fresh installs intentionally contain no provider or terminal-preset seeds.
- **JSON edits**: this repo uses `tidwall/gjson` + `tidwall/sjson` for surgical JSON mutation (config files, manifests) rather than unmarshal-mutate-marshal in many places. Match that style for partial edits.
- **Code & docs are bilingual** (Chinese + English) — follow the surrounding file's language.
- **`agent-outputs/` is a git-tracked archive of historical design material and session reports** (referenced as evidence by `CHANGELOG.md`, `THIRD_PARTY_LICENSES.md`, and `docs/developer/` contract docs — do not move existing files); `.amagi/` and `projects-memory/` are runtime artifacts and stay out of the repo. The `.amagi-codebox/frontend-redesign` handoff doc in `demo/` describes a prior frontend rework.
- Vendored deps are committed (`vendor/`); builds use `-mod=vendor` semantics — add new Go deps via `go get` then `go mod vendor`.
