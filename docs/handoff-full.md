# Leeforest Gateway Handoff

## Project Summary

A custom Go gateway server on a Debian 12 VPS that handles TLS termination, reverse proxying, and child app supervision for the leeforest.dev domain. Child apps are standalone binaries supervised by the gateway with zero-downtime hot-reload via SIGHUP.

---

## VPS Details

- Host: server1.leeforest.dev
- IP: 66.29.133.233
- OS: Debian 12, kernel 6.1.0-52-amd64
- Provider: Spark Hosting
- User: leeforest
- SSH alias: ssh leeforestvps (key-based, root disabled, password auth disabled)

---

## Architecture Overview

### Components

**Gateway (leeforest)**
- Binary: /opt/leeforest/leeforest
- PID file: /opt/leeforest/leeforest.pid
- User: leeforest
- Ports: HTTP 80, HTTPS 443
- Config: /opt/leeforest/config.json
- Cert Cache: /opt/leeforest/certs
- Static Files: /opt/leeforest/www

**Child Apps**
- Location: /opt/leeforest/apps/<appname>/<appname>
- Protocol: Plain HTTP on localhost port
- Supervision: Spawned/restarted by gateway, no systemd units for child apps

**Git Repositories**
- Gateway repo: /opt/git/leeforest.git (bare repo, push-to-deploy hook)
- Child app repos: /opt/git/<appname>.git (bare repos, post-receive hooks)
- GitHub mirror: github.com/lee8oi/leeforest

**Scripts**
- /opt/leeforest/scripts/reload-gateway.sh — triggers config reload via SIGHUP

### Systemd

- Service: leeforest.service at /etc/systemd/system/leeforest.service
- ProtectSystem=strict with ReadWritePaths=/opt/leeforest /var/log/sysconsole
- AmbientCapabilities=CAP_NET_BIND_SERVICE (for binding ports 80/443)
- NoNewPrivileges=yes, ProtectHome=yes, PrivateTmp=yes

---

## Gateway Codebase Structure

    cmd/
    └── gateway/
        └── main.go          # Entry point, loads config and starts server
    internal/
    ├── config/
    │   └── config.go        # Config structs and Load() function
    ├── router/
    │   └── router.go        # Reverse proxy routing, hot-swappable via Reload()
    └── server/
        └── server.go        # TLS handling, Autocert, child app supervision, SIGHUP reload

### Key Modules

**config/config.go**
- Structs: Config, Site, APIRoute
- Fields: Domain, StaticRoot, CertCache, ListenHTTPS, ListenHTTP, Sites[], APIRoutes[]
- Site fields: hostname, upstream_port, binary_path, strip_prefix
- Load(path) reads and validates JSON config
- Validates required fields: domain, static_root, cert_cache
- Defaults: listen_https=":443", listen_http=":80"

**router/router.go**
- Thread-safe reverse proxy router with hot-swap capability
- Reload(cfg) rebuilds proxy maps in-place while serving traffic
- Uses sync.RWMutex to protect concurrent access during reload
- Supports subdomain routing (vhost) via sites map
- Supports API path routing via apiRoutes slice
- Sets X-Forwarded-For header with real client IP via newProxy() helper
- Falls back to static file server for unmatched requests

**server/server.go**
- Manages HTTP/HTTPS listeners with Autocert (Let's Encrypt)
- Dynamic host policy: reads hostnames from config on every TLS handshake
- Spawns child apps via os/exec, monitors health, restarts on crash
- Handles SIGHUP for config reload without restart
- Tracks running apps in map[string]*appInfo with individual cancel contexts
- Each app gets its own cancellable context derived from parent appCtx
- Exponential backoff on crash: 1s, 2s, 4s, ... up to 30s max
- Writes PID file on startup, removes on shutdown
- Graceful shutdown: cancels child app contexts, waits, then shuts down HTTP servers
- SIGHUP handler loops to allow multiple reloads without restart
- Child app stdout/stderr piped to gateway output (visible in journalctl)

---

## Config Structure

    {
      "domain": "leeforest.dev",
      "static_root": "/opt/leeforest/www",
      "cert_cache": "/opt/leeforest/certs",
      "listen_https": ":443",
      "listen_http": ":80",
      "redirect_http": true,
      "sites": [
        {
          "hostname": "<subdomain>.leeforest.dev",
          "upstream_port": <PORT>,
          "binary_path": "/opt/leeforest/apps/<appname>/<appname>",
          "strip_prefix": false
        }
      ],
      "api_routes": []
    }

**Important**: config.json is in .gitignore and must NOT be committed. It lives only on the VPS at /opt/leeforest/config.json. A config.json.example exists in the repo as reference.

---

## Current Child Apps

### status (port 8081)
- Subdomain: status.leeforest.dev
- Binary: /opt/leeforest/apps/status/status
- Repo: /opt/git/status.git
- GitHub: standalone repo
- Function: Simple status page showing hostname, uptime, Go version, CPU cores
- Endpoints: GET / (HTML), GET /health (JSON)
- Stdlib only, no external dependencies

### sysconsole (port 8083)
- Subdomain: sysconsole.leeforest.dev
- Binary: /opt/leeforest/apps/sysconsole/sysconsole
- Repo: /opt/git/sysconsole.git
- Details: Previously existing app, details TBD

---

## Port Allocations

- 8081: status
- 8083: sysconsole
- 8084: testapp (temporary, removed)
- Recommend: new apps use 8082, 8085+

---

## Git Configuration

### Local Repository (WSL)

- Path: ~/code/leeforest (symlinked to /mnt/c/Users/lee8o/Code/leeforest)
- Remote: origin
  - Fetch: git@github.com:lee8oi/leeforest.git
  - Push 1: git@github.com:lee8oi/leeforest.git
  - Push 2: ssh://leeforestvps/opt/git/leeforest.git
- Single push command deploys to both GitHub and VPS: git push origin main
- .gitattributes: * text=auto (line ending normalization for WSL/Windows)
- .gitignore includes config.json
- Branch: main

### Post-Receive Hook (Gateway)

Located at /opt/git/leeforest.git/hooks/post-receive:

    #!/bin/bash
    set -e
    TARGET="/opt/leeforest"
    GIT_DIR="/opt/git/leeforest.git"
    while read oldrev newrev ref; do
        branch=$(echo "$ref" | sed 's|refs/heads/||')
        if [ "$branch" = "main" ]; then
            echo "Deploying main branch..."
            git --work-tree="$TARGET" --git-dir="$GIT_DIR" checkout -f main
            cd "$TARGET"
            echo "Building binary..."
            go build -o leeforest ./cmd/gateway
            if [ $? -ne 0 ]; then
                echo "Build failed! Aborting deployment."
                exit 1
            fi
            echo "Build successful."
            echo "Restarting service..."
            sudo /usr/bin/systemctl restart leeforest
            echo "Deployment complete."
        fi
    done

Note: The gateway hook uses systemctl restart because the gateway binary itself needs a full restart to pick up code changes. Child app hooks use SIGHUP instead (see below).

### Child App Post-Receive Hook Template

    #!/bin/bash
    set -e
    APP_NAME="<appname>"
    TARGET="/opt/leeforest/apps/$APP_NAME"
    GIT_DIR="/opt/git/$APP_NAME.git"
    while read oldrev newrev ref; do
        branch=$(echo "$ref" | sed 's|refs/heads/||')
        if [ "$branch" = "main" ]; then
            echo "Deploying $APP_NAME..."
            mkdir -p "$TARGET"
            git --work-tree="$TARGET" --git-dir="$GIT_DIR" checkout -f main
            cd "$TARGET"
            echo "Building binary..."
            go build -o "$APP_NAME" .
            if [ $? -ne 0 ]; then
                echo "Build failed!"
                exit 1
            fi
            echo "Build successful."
            kill -SIGHUP "$(cat /opt/leeforest/leeforest.pid)"
            echo "Gateway signaled to reload."
        fi
    done

---

## Reload Script

Located at /opt/leeforest/scripts/reload-gateway.sh (also tracked in repo at deploy/reload-gateway.sh):

- Reads PID from /opt/leeforest/leeforest.pid
- Sends SIGHUP to gateway process
- Verifies gateway survived the signal
- Runs as leeforest user, no sudo needed
- Usage: /opt/leeforest/scripts/reload-gateway.sh

---

## Child App Management

### Adding a Child App

1. Build Go binary that listens on 127.0.0.1:<PORT>
2. Copy binary to /opt/leeforest/apps/<appname>/<appname>
3. Add A record on Namecheap: subdomain → 66.29.133.233
4. Add site entry to /opt/leeforest/config.json
5. Run /opt/leeforest/scripts/reload-gateway.sh
6. Gateway spawns the app, adds subdomain to Autocert whitelist, routes HTTPS traffic

### Removing a Child App

1. Remove site entry from /opt/leeforest/config.json
2. Run /opt/leeforest/scripts/reload-gateway.sh
3. Gateway gracefully stops the app, removes routing and TLS whitelist entry
4. Optional: rm -rf /opt/leeforest/apps/<appname> and remove DNS record

### Verified Working

- Adding apps via SIGHUP: tested and confirmed
- Removing apps via SIGHUP: tested and confirmed (after fixing nil cancel function bug)
- Dynamic TLS provisioning: tested and confirmed (Autocert fetched cert for new subdomain on first request)
- Router hot-swap: tested and confirmed (new routes work immediately after reload)
- Zero-downtime reload: confirmed (existing apps continue running during reload)

Full documentation: docs/child-app.md in the gateway repo

---

## SIGHUP Reload Flow

When the gateway receives SIGHUP:

1. Loads fresh config.json from /opt/leeforest/config.json
2. Updates s.cfg under mutex
3. Calls s.router.Reload(cfg) — rebuilds proxy maps in-place (thread-safe)
4. Calls s.reconcileApps(ctx) — compares config.Sites against running apps:
   - Starts new apps that are in config but not running
   - Stops apps that are running but not in config (via individual context cancel)
5. Logs active sites
6. Dynamic host policy picks up new hostnames on next TLS handshake
7. No HTTP server restart, no existing app disruption

---

## Autocert / TLS

- Manager created once at startup
- HostPolicy: dynamic function s.hostPolicy(ctx, host) — reads s.cfg under RLock on every TLS handshake
- Allowed hosts: domain, www.domain, and all site hostnames from config
- Certificate cache: /opt/leeforest/certs
- New subdomains get certs automatically on first HTTPS request
- Removed subdomains are rejected at TLS handshake (not in whitelist)

---

## Known Issues and Past Bugs (Resolved)

### 1. Missing return in router.go handle()

The subdomain proxy handler was missing a return statement after proxying, causing fall-through to API routes and static file serving. Fixed — return added.

### 2. Static Autocert whitelist

Originally hardcoded to only domain and www.domain. Fixed — dynamic host policy reads from config.

### 3. config.json overwritten by git push

Post-receive hook does git checkout -f to /opt/leeforest/ which overwrites config.json. Fixed — added config.json to .gitignore.

### 4. Systemd ProtectSystem=strict

Gateway could not write PID file to /opt/leeforest/. Fixed — expanded ReadWritePaths from /opt/leeforest/certs to /opt/leeforest.

### 5. Nil cancel function panic

When removing a child app, stopApp called info.cancel() but appInfo.cancel was never assigned. Each app now gets its own cancellable context derived from parentCtx, and the cancel function is stored in appInfo at creation time.

### 6. Router pointer swap not working

reload() replaced s.router with a new Router, but HTTP servers held the old pointer. Fixed — router now has a Reload(cfg) method that updates internal maps in-place under mutex.

### 7. Context timeout killing new apps

reconcileApps created a context with 30s timeout that got cancelled when the function returned. Fixed — uses the long-lived appCtx from Start().

### 8. WSL git ownership

Git rejected operations on /mnt/c repos from WSL due to dubious ownership. Fixed with git config --global --add safe.directory.

### 9. Line ending normalization

Files showed as modified when switching between Windows and WSL. Fixed with .gitattributes (* text=auto) and git config --global core.autocrlf input.

### 10. Multiple git push URLs

Origin had mixed HTTPS and SSH push URLs causing GitHub password prompts. Fixed — cleared with git config --unset-all remote.origin.pushurl and re-added as SSH.

---

## Development Environment

- WSL (Debian/Ubuntu) on Windows 11 Pro
- WSL user: lee8oi, hostname: Hellatitude
- Windows user: lee8o
- Git configured with core.autocrlf input and safe.directory for /mnt/c repos
- Project path: ~/code/leeforest (symlinked to /mnt/c/Users/lee8o/Code/leeforest)
- Cross-compile: GOOS=linux GOARCH=amd64 CGO_ENABLED=0

---

## WebSocket Support

- httputil.ReverseProxy handles WebSocket upgrades automatically (since Go 1.12)
- Gateway timeouts: ReadTimeout 5s, WriteTimeout 10s, IdleTimeout 120s
- These timeouts will kill long-lived WebSocket connections
- Mitigation: client sends heartbeat ping every 20-30s to reset IdleTimeout
- Auto-reconnect with localStorage session token for dropped connections
- For truly stateful persistent connections (shells, REPLs), consider per-handler deadline clearing (future work)

---

## Future Work Discussed

### Text-Based Virtual World (leeworld)
- Concept: Social chat-style console with text-based virtual world and user interactions
- Would use WebSocket connections through the gateway
- Requires: world model (rooms, exits, items), player sessions map, room broadcasting, tick loop, heartbeat handler
- Client: WebSocket with heartbeat ping, auto-reconnect, localStorage session token
- Not yet started — design spec created in conversation

### Lumo-to-14B Coding Workflow
- Design apps in Lumo, delegate implementation tasks to local qwen2.5-coder:14b model
- Templates created: handoff template for 14B coding tasks, child app design spec
- Templates stored locally, not in repo
- Local AI setup: Continue extension in VS Code orchestrating Ollama models (Qwen 1.5B autocomplete, Qwen 7B chat/code, Nomic Embed for indexing)

### Potential Improvements
- Per-handler deadline clearing for WebSocket apps (instead of global timeout relaxation)
- Gateway admin API for listing apps, checking status, triggering reloads programmatically
- Health check endpoint on the gateway itself (separate from child app health)
- Config validation on reload (reject bad configs before applying)
- Binary change detection in reconcileApps (restart apps whose binary file changed)

---

## Quick Reference Commands

**Deploy gateway code changes:**

    git push origin main

**Reload gateway config (no restart):**

    /opt/leeforest/scripts/reload-gateway.sh

**Check gateway logs:**

    sudo journalctl -u leeforest -n 20 --no-pager

**Check running child apps:**

    sudo journalctl -u leeforest -n 20 --no-pager | grep "Started app"

**Restart gateway (full, use sparingly):**

    sudo systemctl restart leeforest

**Test a child app locally:**

    curl http://127.0.0.1:<PORT>

**Test through gateway with TLS:**

    curl https://<subdomain>.leeforest.dev

**Edit gateway config:**

    nano /opt/leeforest/config.json

**Reload systemd after service file changes:**

    sudo systemctl daemon-reload

---

## References

- Gateway repo: github.com/lee8oi/leeforest
- Gateway docs: docs/child-app.md (in repo)
- Reload script: deploy/reload-gateway.sh (in repo, copied to /opt/leeforest/scripts/)
- Service file: deploy/leeforest.service (in repo, installed at /etc/systemd/system/)
- Deployment script: deployment.sh (in repo, for initial server setup)
- Makefile: build, deploy, deploy-www, deploy-config targets
- local-build.sh: quick reference for cross-compile and deploy workflow
