# Leeforest Gateway — Architecture Reference

> Stable reference for developers and AI assistants working on the gateway codebase. Reviewed from source files as of handoff.

## System Overview

The Leeforest Gateway is a Go application providing TLS termination, reverse proxy routing, and child app supervision for the `leeforest.dev` domain ecosystem. It runs as a systemd service on Debian 12, handling both HTTPS (443) and HTTP (80) traffic with automatic Let's Encrypt certificate provisioning via Autocert.

### Core Responsibilities

| Responsibility | Implementation |
|---|---|
| TLS termination | `autocert.Manager` with dynamic host policy |
| Reverse proxy routing | `http.ServeMux` wrapped by custom `Router` |
| Child app supervision | `exec.Command` with exponential backoff restart |
| Hot config reload | SIGHUP handler with mutex-protected config swap |
| Process management | PID file, systemd integration, graceful shutdown |

## Codebase Structure

    cmd/
    └── gateway/
        └── main.go          # Entry point: loads config, starts server

    internal/
    ├── config/
    │   └── config.go        # Config structs, Load(), validation
    ├── router/
    │   └── router.go        # Thread-safe reverse proxy router with hot-swap
    └── server/
        └── server.go        # HTTP/HTTPS listeners, Autocert, app supervision, SIGHUP

### Go Module

- Module: `github.com/lee8oi/leeforest`
- Go version: 1.26.6
- Sole dependency: `golang.org/x/crypto v0.55.0` (for `autocert`)
- Transitive: `golang.org/x/net v0.57.0`, `golang.org/x/text v0.41.0`

### Module Deep-Dives

**cmd/gateway/main.go**
- Minimal entry point (~15 lines)
- Parses `-config` flag (defaults to `/opt/leeforest/config.json`)
- Calls `config.Load()` for parsing and validation
- Creates `server.Server`, calls `Start()` which blocks until shutdown

**internal/config/config.go**
- Defines `Config`, `Site`, and `APIRoute` structs with JSON tags

    type Config struct {
        Domain       string     `json:"domain"`
        StaticRoot   string     `json:"static_root"`
        CertCache    string     `json:"cert_cache"`
        ListenHTTPS  string     `json:"listen_https"`
        ListenHTTP   string     `json:"listen_http"`
        RedirectHTTP bool       `json:"redirect_http"`
        Sites        []Site     `json:"sites"`
        APIRoutes    []APIRoute `json:"api_routes"`
    }

    type Site struct {
        Hostname     string `json:"hostname"`
        UpstreamPort int    `json:"upstream_port"`
        BinaryPath   string `json:"binary_path"`
        StripPrefix  bool   `json:"strip_prefix"`
    }

    type APIRoute struct {
        Path         string `json:"path"`
        UpstreamPort int    `json:"upstream_port"`
        StripPrefix  bool   `json:"strip_prefix"`
    }

- `Load(path)` reads file, unmarshals JSON, calls `validate()`
- Validation: `domain`, `static_root`, `cert_cache` required; `listen_https` and `listen_http` default to `:443` and `:80` if empty
- No defaults for `Sites` or `APIRoutes` — empty arrays are valid

**internal/router/router.go**
- Wraps `http.ServeMux` with vhost and API path awareness
- `Router` struct fields:

    type Router struct {
        mux        *http.ServeMux
        staticRoot string
        mu         sync.RWMutex
        sites      map[string]*httputil.ReverseProxy
        apiRoutes  []apiEntry
    }

- `New(cfg)` creates router, calls `build(cfg)`, registers `handle` on `/`
- `build(cfg)` constructs proxy maps from config under mutex lock
- `Reload(cfg)` calls `build(cfg)` — updates maps in-place, no pointer replacement (fixes Bug #6)
- `newProxy(target)` creates `httputil.ReverseProxy` with custom Director that appends real client IP to `X-Forwarded-For`
- `ServeHTTP` delegates to internal mux
- `handle(w, req)` dispatches in order:
  1. Subdomain proxies — splits `req.Host` on `:` to get hostname, looks up in `sites` map
  2. API path routes — iterates `apiRoutes`, matches by path prefix
  3. Static file server — falls back to `http.FileServer` serving from `staticRoot`
- Uses `sync.RWMutex`: RLock for request handling, Lock for build/reload

**internal/server/server.go**
- `Server` struct fields:

    type Server struct {
        cfg       *config.Config
        router    *router.Router
        apps      map[string]*appInfo
        appCtx    context.Context
        appCancel context.CancelFunc
        manager   *autocert.Manager
        mu        sync.RWMutex
    }

    type appInfo struct {
        cancel context.CancelFunc
        pid    int
    }

- Constants: `pidFilePath = "/opt/leeforest/leeforest.pid"`, `configPath = "/opt/leeforest/config.json"`
- `hostPolicy(ctx, host)` — reads `s.cfg` under RLock on every TLS handshake, builds allowed-hosts set from domain, www.domain, and all site hostnames
- `spawnApp(parentCtx, binary, wg)` — creates per-app cancellable context, stores `appInfo` in map, runs binary via `exec.CommandContext`, pipes stdout/stderr to gateway output, restarts on exit with exponential backoff (1s → 2s → 4s → ... max 30s), exits when context cancelled
- `stopApp(binary)` — retrieves `appInfo`, calls `cancel()` to trigger graceful shutdown
- `reconcileApps(ctx)` — snapshots running apps, iterates config sites to find new apps to start, iterates running apps to find removed apps to stop, sleeps 500ms for startup
- `reload()` — loads fresh config from disk, updates `s.cfg` under mutex, calls `s.router.Reload(cfg)`, calls `s.reconcileApps(ctx)` using existing `appCtx`, logs active sites
- `writePIDFile()` — writes current PID to pidFilePath (0644 permissions)
- `Start()` — full lifecycle:
  1. Write PID file (defer removal on exit)
  2. Create cert cache directory (0700)
  3. Configure Autocert manager with dynamic host policy
  4. Create long-lived `appCtx`/`appCancel`
  5. Configure HTTPS server (ReadTimeout 5s, WriteTimeout 10s, IdleTimeout 120s)
  6. Configure HTTP server (redirect to HTTPS or ACME-only)
  7. Spawn initial child apps with WaitGroup
  8. Signal handling loop: SIGHUP → reload, SIGINT/SIGTERM → shutdown
  9. On shutdown: cancel appCtx, wait for WaitGroup, shut down HTTP servers with 30s timeout

## Request Lifecycle

1. Client connects to port 443 (HTTPS)
2. TLS handshake begins; `hostPolicy` checks hostname against current config
3. If host not in whitelist → TLS rejected
4. If host in whitelist → Autocert serves cached cert or provisions new one
5. HTTP request routed to `Router.ServeHTTP` → `mux.ServeHTTP` → `handle()`
6. `handle()` splits Host header to get hostname
7. If hostname matches a `sites` entry → reverse proxy to `127.0.0.1:<port>`
8. If no subdomain match, check `apiRoutes` by path prefix → reverse proxy if matched
9. If no match → serve static files from `staticRoot`

For HTTP (port 80):
- ACME challenge paths pass through to Autocert manager
- All other requests redirect to HTTPS (if `redirect_http` is true)

## SIGHUP Reload Flow

1. Signal received in `Start()` loop
2. `reload()` called:
   a. `config.Load(configPath)` reads fresh config from disk
   b. `s.cfg` updated under mutex
   c. `s.router.Reload(cfg)` rebuilds proxy maps in-place (thread-safe)
   d. `s.reconcileApps(s.appCtx)` compares config against running apps:
      - Starts new apps not currently running
      - Stops running apps not in new config (via context cancel)
   e. Logs active sites
3. Dynamic host policy picks up new hostnames on next TLS handshake
4. No HTTP server restart, no disruption to existing apps

## Child App Lifecycle

**Spawn:**
- `reconcileApps` detects new site in config → calls `spawnApp` in goroutine
- `spawnApp` creates per-app cancellable context from `appCtx`
- Stores `appInfo{cancel, pid}` in `s.apps` map keyed by binary path
- Runs `exec.CommandContext(appCtx, binary)`
- Pipes stdout/stderr to gateway output (visible in journalctl)
- Logs "Started app: <binary> (PID <pid>)"

**Supervision:**
- `cmd.Wait()` blocks until app exits
- If context was cancelled → app stopped during shutdown, return
- If app exited with error → log, restart with backoff
- If app exited cleanly → log, restart with backoff
- Backoff: starts at 1s, doubles each crash, capped at 30s
- `sleepWithContext` allows early return if context cancelled during backoff wait

**Graceful Stop:**
- `stopApp(binary)` retrieves `appInfo`, calls `info.cancel()`
- Context cancellation propagates to `exec.CommandContext` (kills process)
- `spawnApp`'s `cmd.Wait()` returns, `appCtx.Err() != nil` detected
- `appInfo` removed from `s.apps` map, WaitGroup decremented

## Autocert / TLS Model

- Single `autocert.Manager` created at startup
- `Prompt: autocert.AcceptTOS` — automatically accepts Let's Encrypt TOS
- `Cache: autocert.DirCache(s.cfg.CertCache)` — certificates stored at `/opt/leeforest/certs`
- `HostPolicy: s.hostPolicy` — dynamic function, not static list
- `hostPolicy` reads `s.cfg` under RLock on every TLS handshake
- Allowed hosts: `domain`, `www.domain`, and all `site.Hostname` values
- New subdomains: cert provisioned automatically on first HTTPS request (takes a few seconds)
- Removed subdomains: rejected at TLS handshake (no longer in whitelist)
- Certificate renewal: handled automatically by Autocert/Let's Encrypt lifecycle
- Renewal failure handling: not explicitly configured — depends on Autocert default behavior (renews before expiry)

## WebSocket Support

- `httputil.ReverseProxy` handles WebSocket upgrades automatically (since Go 1.12)
- Gateway timeouts that affect long-lived connections:
  - ReadTimeout: 5 seconds
  - WriteTimeout: 10 seconds
  - IdleTimeout: 120 seconds
- IdleTimeout will close connections with no activity after 120s
- Mitigation: client sends heartbeat ping every 20-30s to reset IdleTimeout
- Auto-reconnect recommended with localStorage session token
- For stateful persistent connections (shells, REPLs): per-handler deadline clearing is future work

## Concurrency Model

**Mutex usage:**
- `Server.mu` (sync.RWMutex): protects `s.cfg`, `s.apps` map
  - RLock: `hostPolicy`, `reconcileApps` (snapshot), `reload` (reading appCtx)
  - Lock: `spawnApp` (write appInfo), `stopApp`, `reload` (writing cfg), deferred cleanup in `spawnApp`
- `Router.mu` (sync.RWMutex): protects `r.sites`, `r.apiRoutes`, `r.staticRoot`
  - RLock: `handle` (request path)
  - Lock: `build` (during construction and reload)

**Context tree:**
- `context.Background()` → root
  - `appCtx` (server lifetime, cancelled on SIGINT/SIGTERM)
    - Per-app contexts (one per spawned app, cancelled individually via `appInfo.cancel`)
    - Also passed to `exec.CommandContext` for process kill propagation

**Goroutines:**
- HTTP listener goroutine
- HTTPS listener goroutine
- One `spawnApp` goroutine per child app (long-lived, loops on restart)
- Two `io.Copy` goroutines per app instance (stdout, stderr pipes)

## Security Model

**Systemd hardening:**

    NoNewPrivileges=yes
    ProtectSystem=strict
    ReadWritePaths=/opt/leeforest/certs
    ProtectHome=yes
    PrivateTmp=yes
    AmbientCapabilities=CAP_NET_BIND_SERVICE
    CapabilityBoundingSet=CAP_NET_BIND_SERVICE

**WARNING — Known service file discrepancy:** The repo copy of `deploy/leeforest.service` has `ReadWritePaths=/opt/leeforest/certs` only, but `server.go` writes its PID file to `/opt/leeforest/leeforest.pid`. The handoff documents this was expanded to `/opt/leeforest` after a past bug (#4). Either the repo copy is stale or the VPS copy was manually patched. **Verify the actual service file on the VPS.**

**Network exposure:**
- Ports 80 and 443 bound via `CAP_NET_BIND_SERVICE` (no root needed)
- Child apps listen on `127.0.0.1:<port>` only — not externally reachable
- All external traffic must pass through gateway (TLS termination + routing)

**Access control:**
- SSH: key-based only, root login disabled, password auth disabled
- Gateway runs as unprivileged `leeforest` user
- No gateway admin API exists (future work)
- Config file readable by `leeforest` user only (not in git)

## Timeout Summary

| Timeout | Value | Scope | Effect |
|---|---|---|---|
| ReadTimeout | 5s | HTTPS server | Max time to read request headers/body |
| WriteTimeout | 10s | HTTPS server | Max time to write response |
| IdleTimeout | 120s | HTTPS server | Max idle keep-alive duration |
| ReadTimeout | 5s | HTTP server | Max time to read request |
| WriteTimeout | 5s | HTTP server | Max time to write response |
| Shutdown timeout | 30s | Both servers | Max wait during graceful shutdown |
| App backoff | 1s→30s | Per child app | Restart delay after crash (exponential) |
| Reconcile sleep | 500ms | reconcileApps | Wait for new apps to start |

## Key Design Decisions

1. **In-place router reload** (not pointer swap) — HTTP servers hold the router reference; replacing the pointer would leave servers using the old router. `Reload()` updates internal maps under mutex instead.

2. **Per-app cancellable contexts** — Each app gets its own context derived from `appCtx`, enabling individual stop without affecting siblings. The cancel function is stored in `appInfo` at creation time (fixes Bug #5 — nil cancel panic).

3. **Long-lived appCtx for reconciliation** — `reconcileApps` uses the server-lifetime `appCtx`, not a function-scoped context (fixes Bug #7 — context timeout killing new apps).

4. **Dynamic host policy as function** — Reads config on every TLS handshake rather than static list, allowing subdomain additions/removals to take effect without server restart.

5. **SIGHUP loop in signal handler** — The `for` loop around `<-shutdownChan` allows multiple SIGHUP reloads without restart. SIGINT/SIGTERM break out via `goto shutdown`.

6. **Static file server created per-request** — In `handle()`, the static file server is instantiated on every request rather than cached. This is a minor inefficiency but ensures `staticRoot` changes take effect after reload.

7. **Binary path as app key** — Apps are keyed by `binary_path` in the `apps` map, not by hostname. This means two hostnames pointing to the same binary share a single app instance.

## Resolved Bugs Archive

1. **Missing return in `handle()`** — Subdomain proxy fell through to API routes and static serving. Fixed with explicit `return` after `proxy.ServeHTTP`.

2. **Static Autocert whitelist** — Originally hardcoded to domain and www only. Fixed with dynamic `hostPolicy` function reading from config.

3. **`config.json` overwritten by git push** — Post-receive hook's `git checkout -f` overwrote config.json. Fixed by adding to `.gitignore`.

4. **Systemd ProtectSystem=strict blocking PID file** — Could not write PID file to `/opt/leeforest/`. Fixed by expanding `ReadWritePaths`. **Note: repo service file may not reflect this fix — verify on VPS.**

5. **Nil cancel function panic** — `stopApp` called `info.cancel()` but `appInfo.cancel` was never assigned. Fixed by deriving per-app context at spawn time and storing cancel in `appInfo`.

6. **Router pointer swap not working** — `reload()` replaced `s.router` with new `Router`, but HTTP servers held old pointer. Fixed by adding `Reload(cfg)` method that updates internal maps in-place.

7. **Context timeout killing new apps** — `reconcileApps` created a 30s timeout context that was cancelled when function returned. Fixed by using the long-lived `appCtx` from `Start()`.

8. **WSL git ownership** — Git rejected `/mnt/c` repos from WSL due to dubious ownership. Fixed with `git config --global --add safe.directory`.

9. **Line ending normalization** — Files showed as modified between Windows and WSL. Fixed with `.gitattributes` (`* text=auto`) and `git config --global core.autocrlf input`.

10. **Multiple git push URLs** — Origin had mixed HTTPS and SSH URLs causing password prompts. Fixed by clearing and re-adding as SSH-only.
