# Leeforest Gateway — Operations Manual

> Practical guide for deploying, configuring, and operating the gateway. Companions file to GATEWAY-ARCHITECTURE.md.

## VPS Details

- Host: server1.leeforest.dev
- IP: 66.29.133.233
- OS: Debian 12, kernel 6.1.0-52-amd64
- Provider: Spark Hosting
- User: leeforest
- SSH alias: `ssh leeforestvps` (key-based, root disabled, password auth disabled)

## Directory Layout

    /opt/leeforest/
    ├── leeforest              # Gateway binary
    ├── leeforest.pid          # PID file (written at startup, removed at shutdown)
    ├── config.json            # Live configuration (NOT in git)
    ├── certs/                 # Autocert certificate cache (0700 perms)
    ├── www/                   # Static file root
    ├── apps/                  # Child app binaries
    │   └── <appname>/
    │       └── <appname>      # Compiled binary
    └── scripts/
        └── reload-gateway.sh  # SIGHUP reload script

    /opt/git/
    ├── leeforest.git          # Gateway bare repo (push-to-deploy)
    ├── <appname>.git          # Child app bare repos (push-to-deploy)

    /etc/systemd/system/
    └── leeforest.service      # Systemd unit file

## Development Environment

- WSL (Debian/Ubuntu) on Windows 11 Pro
- WSL user: lee8oi, hostname: Hellatitude
- Windows user: lee8o
- Project path: `~/code/leeforest` (symlinked to `/mnt/c/Users/lee8o/Code/leeforest`)
- Cross-compile: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`
- Git config: `core.autocrlf input`, `safe.directory` for `/mnt/c` repos

## Git Repository Topology

### Local Repository

    Remote: origin
      Fetch: git@github.com:lee8oi/leeforest.git
      Push 1: git@github.com:lee8oi/leeforest.git
      Push 2: ssh://leeforestvps/opt/git/leeforest.git

A single `git push origin main` deploys to both GitHub (mirror) and VPS (deploy). This dual-push setup means code changes trigger the post-receive hook on the VPS automatically.

- Branch: `main`
- `.gitattributes`: `* text=auto` (line ending normalization for WSL/Windows)
- `.gitignore`: excludes `config.json`, `bin/`, editor files, OS files

### .gitignore (full content)

    # Binaries
    bin/

    # Local config overrides (production secrets)
    config.local.json
    config.production.json
    config.json

    # Editor files
    .vscode/
    .idea/
    *.swp
    *.swo
    *~

    # OS files
    .DS_Store
    Thumbs.db

### .gitattributes (full content)

    * text=auto

## Deployment Workflows

### Gateway Code Deployment

Push to main triggers the post-receive hook:

    git push origin main

The hook checks out code, builds the binary, and restarts the service.

**Post-receive hook** (at `/opt/git/leeforest.git/hooks/post-receive`):

    #!/bin/bash
    TARGET="/opt/leeforest"
    GIT_DIR="/opt/git/leeforest.git"

    while read oldrev newrev ref; do
        branch=$(echo "$ref" | sed 's|refs/heads/||')
        if [ "$branch" = "main" ]; then
            echo "Deploying main branch..."
            git --work-tree="$TARGET" --git-dir="$GIT_DIR" checkout -f main

            # Build the Go binary
            cd "$TARGET"
            echo "Building binary..."
            go build -o leeforest ./cmd/gateway
            if [ $? -ne 0 ]; then
                echo "Build failed! Aborting deployment."
                exit 1
            fi
            echo "Build successful."

            # Restart the service
            echo "Restarting service..."
            sudo /usr/bin/systemctl restart leeforest
            echo "Deployment complete."
        fi
    done

**Key difference from child app hooks:** Gateway uses `systemctl restart` (full process restart) because the gateway binary itself changes. Child app hooks use SIGHUP (hot reload) because only config/routes change.

### Alternative: Make-based Deployment

The Makefile provides manual deploy targets (alternative to git push):

    APP_NAME := leeforest
    BUILD_DIR := bin
    REMOTE_USER := leeforest
    REMOTE_HOST := 66.29.133.233
    REMOTE_PATH := /opt/leeforest

**Targets:**

- `make build` — Cross-compiles to `bin/leeforest`
- `make run` — Builds and runs locally with `config.json`
- `make clean` — Removes `bin/`
- `make deploy` — Builds, SCPs binary to VPS, restarts service
- `make deploy-www` — Rsyncs `www/` to VPS static root
- `make deploy-config` — SCPs `config.json` to VPS, restarts service

Full Makefile content:

    .PHONY: build clean deploy run

    APP_NAME := leeforest
    BUILD_DIR := bin
    REMOTE_USER := leeforest
    REMOTE_HOST := 66.29.133.233
    REMOTE_PATH := /opt/leeforest

    build:
        mkdir -p $(BUILD_DIR)
        GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/gateway

    run: build
        ./$(BUILD_DIR)/$(APP_NAME) -config config.json

    clean:
        rm -rf $(BUILD_DIR)

    deploy: build
        scp $(BUILD_DIR)/$(APP_NAME) $(REMOTE_USER)@$(REMOTE_HOST):/tmp/$(APP_NAME).new
        ssh $(REMOTE_USER)@$(REMOTE_HOST) "sudo mv /tmp/$(APP_NAME).new $(REMOTE_PATH)/$(APP_NAME) && sudo systemctl restart leeforest"

    # Sync static files
    deploy-www:
        rsync -avz --delete www/ $(REMOTE_USER)@$(REMOTE_HOST):$(REMOTE_PATH)/www/

    # Sync config
    deploy-config:
        scp config.json $(REMOTE_USER)@$(REMOTE_HOST):/tmp/config.json
        ssh $(REMOTE_USER)@$(REMOTE_HOST) "sudo mv /tmp/config.json $(REMOTE_PATH)/config.json && sudo systemctl restart leeforest"

### Quick Reference: local-build.sh

    # Build for Linux (cross-compile from WSL or Windows)
    make build

    # Set YOUR_VPS_IP in Makefile first, then:
    make deploy
    make deploy-www
    make deploy-config

## Child App Deployment

Child apps are deployed via their own bare git repos with post-receive hooks. See `docs/child-app.md` for the full procedure and hook template.

**Post-receive hook template for child apps:**

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

**Key difference:** Child app hooks end with `kill -SIGHUP` (hot reload) instead of `systemctl restart` (cold restart). The gateway stays running and picks up the new binary via config reconciliation.

## Config Schema Reference

Config lives at `/opt/leeforest/config.json` on the VPS. Example file (`config.json.example`) is in the repo:

    {
      "domain": "example.dev",
      "static_root": "/opt/leeforest/www",
      "cert_cache": "/opt/leeforest/certs",
      "listen_https": ":443",
      "listen_http": ":80",
      "redirect_http": true,
      "sites": [
        {
          "hostname": "app.example.dev",
          "upstream_port": 8081,
          "binary_path": "/opt/leeforest/apps/appname/appname",
          "strip_prefix": false
        }
      ],
      "api_routes": [
        {
          "path": "/api/appname/",
          "upstream_port": 8081,
          "strip_prefix": true
        }
      ]
    }

### Field Reference

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `domain` | string | Yes | — | Primary domain for TLS host policy |
| `static_root` | string | Yes | — | Directory for static file fallback |
| `cert_cache` | string | Yes | — | Autocert certificate cache directory |
| `listen_https` | string | No | `:443` | HTTPS listen address |
| `listen_http` | string | No | `:80` | HTTP listen address |
| `redirect_http` | bool | No | `false` | If true, HTTP redirects to HTTPS (ACME challenges still pass through) |
| `sites` | array | No | `[]` | Subdomain reverse proxy entries |
| `api_routes` | array | No | `[]` | Path-based reverse proxy entries |

**Site fields:**

| Field | Type | Required | Description |
|---|---|---|---|
| `hostname` | string | Yes | Subdomain for vhost matching (e.g., `app.leeforest.dev`) |
| `upstream_port` | int | Yes | Localhost port the child app listens on |
| `binary_path` | string | Yes | Absolute path to child app binary |
| `strip_prefix` | bool | No | Unused for site routes (only affects API routes) |

**APIRoute fields:**

| Field | Type | Required | Description |
|---|---|---|---|
| `path` | string | Yes | Path prefix to match (e.g., `/api/appname/`) |
| `upstream_port` | int | Yes | Localhost port to proxy to |
| `strip_prefix` | bool | No | If true, removes matched path prefix before forwarding |

**IMPORTANT:** `config.json` is in `.gitignore` and must NOT be committed. It exists only on the VPS. `config.json.example` serves as the reference template in the repo.

## Port Allocation Registry

| Port | App | Status |
|---|---|---|
| 8081 | status | Active |
| 8082 | (available) | Reserved for next app |
| 8083 | sysconsole | Active |
| 8084 | testapp | Removed (temporary) |
| 8085+ | (available) | Available for new apps |

Convention: skip 8084 (previously used by temporary testapp). New apps should use 8082, then 8085, 8086, etc.

## Systemd Unit File

Located at `/etc/systemd/system/leeforest.service`. Repo copy at `deploy/leeforest.service`:

    [Unit]
    Description=Lee Forest Gateway Server
    After=network-online.target
    Wants=network-online.target

    [Service]
    Type=simple
    User=leeforest
    Group=leeforest
    WorkingDirectory=/opt/leeforest
    ExecStart=/opt/leeforest/leeforest -config /opt/leeforest/config.json
    Restart=always
    RestartSec=5

    # Hardening
    NoNewPrivileges=yes
    ProtectSystem=strict
    ReadWritePaths=/opt/leeforest/certs
    ProtectHome=yes
    PrivateTmp=yes

    AmbientCapabilities=CAP_NET_BIND_SERVICE
    CapabilityBoundingSet=CAP_NET_BIND_SERVICE

    [Install]
    WantedBy=multi-user.target

**WARNING:** The repo copy has `ReadWritePaths=/opt/leeforest/certs` only, but `server.go` writes its PID file to `/opt/leeforest/leeforest.pid` (parent directory). The handoff states this was expanded to `/opt/leeforest` after Bug #4. **Verify the actual service file on the VPS matches what the gateway needs.** If the VPS copy still has only `/opt/leeforest/certs`, the gateway cannot write its PID file under systemd.

To update the service file:

    # Edit the actual file on the VPS
    sudo nano /etc/systemd/system/leeforest.service
    # Change ReadWritePaths to: /opt/leeforest /opt/leeforest/certs
    sudo systemctl daemon-reload
    sudo systemctl restart leeforest

## Initial Server Setup

The `deployment.sh` script handles initial setup:

    # Create dedicated user
    sudo useradd -r -s /bin/false -d /opt/leeforest leeforest

    # Create directory structure
    sudo mkdir -p /opt/leeforest/{www,certs,apps}
    sudo chown -R leeforest:leeforest /opt/leeforest

    # Install the service file
    sudo cp leeforest.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable leeforest
    sudo systemctl start leeforest

Prerequisites not in the script: Go must be installed on the VPS (for post-receive hooks to compile), SSH key access configured, and git installed.

## Reload Script

Located at `/opt/leeforest/scripts/reload-gateway.sh` (also tracked in repo at `deploy/reload-gateway.sh`):

    #!/bin/bash
    # /opt/leeforest/scripts/reload-gateway.sh
    #
    # Trigger a config reload on the gateway without restarting.
    # Usage: sudo /opt/leeforest/scripts/reload-gateway.sh
    #
    # The gateway catches SIGHUP, reloads config.json, and spawns/stops child apps
    # as needed. Existing apps continue running uninterrupted.

    set -euo pipefail

    SCRIPT_NAME="$(basename "$0")"
    PID_FILE="/opt/leeforest/leeforest.pid"
    LOG_PREFIX="[gateway-reload]"

    log() {
        echo "$LOG_PREFIX $*"
    }

    # Check PID file exists
    if [ ! -f "$PID_FILE" ]; then
        log "ERROR: PID file not found at $PID_FILE"
        log "Is the gateway running?"
        exit 1
    fi

    # Read PID
    PID=$(cat "$PID_FILE")

    # Verify process is alive
    if ! kill -0 "$PID" 2>/dev/null; then
        log "ERROR: Gateway not running (PID $PID appears dead)"
        log "Try: sudo systemctl start leeforest"
        exit 1
    fi

    log "Sending SIGHUP to gateway (PID $PID)..."
    kill -SIGHUP "$PID"

    # Wait briefly for reload to complete
    sleep 0.5

    # Verify gateway survived the signal
    if ! kill -0 "$PID" 2>/dev/null; then
        log "ERROR: Gateway crashed after signal"
        log "Check logs: sudo journalctl -u leeforest -n 20"
        exit 1
    fi

    log "SUCCESS: Gateway reloaded config."
    log "Check child apps: sudo journalctl -u leeforest -n 5 --no-pager | grep 'Started app'"
    exit 0

Runs as `leeforest` user, no sudo needed. Verifies gateway survived the signal. Reports next steps on failure.

## Logging

All gateway and child app output goes to journald via systemd:

    # View recent gateway logs
    sudo journalctl -u leeforest -n 20 --no-pager

    # Follow logs in real-time
    sudo journalctl -u leeforest -f

    # Check which child apps are running
    sudo journalctl -u leeforest -n 20 --no-pager | grep "Started app"

Child app stdout/stderr is piped through the gateway process and appears in the same journald stream. Log messages include:

- `HTTP listening on :80` / `HTTPS listening on :443` — server startup
- `Started app: <binary> (PID <pid>)` — child app spawned
- `App <binary> stopped during shutdown` — graceful shutdown
- `<binary> exited with error: <err>` — child crash
- `<binary> exited cleanly` — child exited without error
- `Restarting <binary> in <backoff>...` — crash recovery
- `Received SIGHUP, reloading config...` — config reload triggered
- `Config reloaded. Active sites:` followed by site list — reload success
- `Config reload failed: <err>` — reload error
- `Stopping app supervisor for <binary>` — app context cancelled

## Troubleshooting Playbook

### Gateway won't start

    sudo systemctl status leeforest
    sudo journalctl -u leeforest -n 30 --no-pager

Common causes:
- Config validation failure (missing required fields)
- Binary not executable or wrong architecture
- Port 80/443 already in use
- systemd ProtectSystem blocking PID file write (check ReadWritePaths)

### SIGHUP reload fails

    sudo journalctl -u leeforest -n 10 --no-pager

Look for `Config reload failed:` message. Common causes:
- `config.json` has invalid JSON
- Required fields missing (domain, static_root, cert_cache)
- File not readable by leeforest user

### Child app not starting

Check if binary exists and is executable:

    ls -la /opt/leeforest/apps/<appname>/<appname>

Check if port is already in use:

    ss -tlnp | grep <PORT>

Check gateway logs for spawn errors.

### TLS certificate not provisioning

Verify DNS A record points to 66.29.133.233. Autocert provisions on first HTTPS request. Check that hostname is in config sites. Look for TLS errors in logs.

### Git push deploy fails

The post-receive hook runs `go build` on the VPS. Ensure Go is installed on the VPS. Check hook output for build errors. Remember: `config.json` is in `.gitignore` so it won't be overwritten by checkout.

### Child app crashes repeatedly

Gateway uses exponential backoff (1s → 2s → 4s → ... max 30s). Check app-specific errors in journald output. The app's stderr is piped through the gateway and visible in `journalctl -u leeforest`.

## Backup and Recovery

### What to back up

- `/opt/leeforest/config.json` — not in git, unique to VPS
- `/opt/git/` — all bare repos with hooks
- `/etc/systemd/system/leeforest.service` — may differ from repo copy
- `/opt/leeforest/certs/` — Autocert cache (can be re-provisioned, but saves time)

### Recovery procedure

1. Re-run `deployment.sh` to recreate user and directories
2. Restore bare repos and hooks to `/opt/git/`
3. Restore `config.json` to `/opt/leeforest/`
4. Restore service file to `/etc/systemd/system/`
5. Push to gateway repo to trigger build and deploy, or `make deploy`
6. Reload config: `/opt/leeforest/scripts/reload-gateway.sh`

**Note:** No automated backup strategy is currently in place. This is a known gap.

## Verification Procedures

### After gateway deployment

    # Service is running
    sudo systemctl status leeforest

    # No errors in recent logs
    sudo journalctl -u leeforest -n 20 --no-pager

    # HTTPS responds
    curl -I https://leeforest.dev

    # HTTP redirects to HTTPS
    curl -I http://leeforest.dev

### After config reload

    # Reload succeeded
    sudo journalctl -u leeforest -n 5 --no-pager

    # Each site responds
    curl -I https://<subdomain>.leeforest.dev

    # Child apps are running
    sudo journalctl -u leeforest -n 10 --no-pager | grep "Started app"

### After child app deployment

    # App is listening locally
    curl http://127.0.0.1:<PORT>

    # App is reachable through gateway
    curl https://<subdomain>.leeforest.dev

## Future Improvements

- **Config validation on reload** — reject bad configs before applying (currently applies then logs error)
- **Binary change detection** — restart apps whose binary file changed during reconcile
- **Gateway health endpoint** — separate from child app health checks
- **Admin API** — list apps, check status, trigger reloads programmatically
- **Per-handler deadline clearing** — for WebSocket apps instead of global timeout relaxation
- **Automated backups** — no strategy currently in place
- **Service file sync** — repo copy may not match VPS copy (ReadWritePaths discrepancy)
