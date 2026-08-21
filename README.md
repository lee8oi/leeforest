# leeforest.dev Gateway Server

A minimal Go-based web gateway with automatic Let's Encrypt TLS termination, 
static file serving, child app spawning, and reverse proxy routing.

Designed for self-hosted deployment on a single VPS. The only external 
dependency is golang.org/x/crypto for Autocert (Let's Encrypt) support.

## Features

- Automatic Let's Encrypt certificates via golang.org/x/crypto/acme/autocert
- HTTP to HTTPS redirect on port 80
- Static file serving from disk (update content without rebuilding)
- Subdomain-based reverse proxy routing
- API path-based reverse proxy routing
- Child app spawning with automatic restart on crash (exponential backoff)
- Dynamic TLS whitelist (reads subdomains from config)
- Hot-reload configuration via SIGHUP (zero-downtime)
- Config validation for sites and API routes on load and reload
- systemd service with auto-restart
- Push-to-deploy via bare Git repository and post-receive hook

## Requirements

- Go 1.26 or newer
- A VPS with ports 80 and 443 open
- A domain name with DNS A records pointing to your VPS

## Quick Start

Clone the repo:

    git clone https://github.com/lee8oi/leeforest.git
    cd leeforest

Build the binary:

    go build -o leeforest ./cmd/gateway

Run locally (use a local config with appropriate listen addresses):

    ./leeforest -config config.json

See docs/local-build.md for local development and testing guidance.

## Project Structure

    leeforest/
    ├── cmd/
    │   └── gateway/
    │       └── main.go          # Entry point
    ├── internal/
    │   ├── config/
    │   │   └── config.go        # Config loading and validation
    │   ├── router/
    │   │   └── router.go        # Vhost + API path routing
    │   └── server/
    │       └── server.go        # TLS, Autocert, child app spawning
    ├── www/                     # Static web root
    │   ├── index.html
    │   └── assets/
    ├── deploy/
    │   ├── leeforest.service    # systemd unit file
    │   ├── deployment.sh        # Initial server setup script
    │   └── reload-gateway.sh    # SIGHUP reload script
    ├── docs/
    │   ├── child-app.md        # Child app management guide
    │   └── local-build.md      # Local build and testing guide
    ├── gateway-structure.md    # Architecture reference
    ├── gateway-operations.md   # Operations manual
    ├── GATEWAY-QUICKREF.md     # Quick reference cheat sheet
    ├── config.json.example     # Reference config (copy to config.json)
    ├── go.mod
    ├── go.sum
    └── Makefile

Note: config.json is gitignored and exists only on the VPS at 
/opt/leeforest/config.json. Use config.json.example as a template.

## Configuration

Copy the example config and customize:

    cp config.json.example config.json

Config fields:

    domain          Primary domain name (required, added to TLS whitelist)
    static_root     Path to static file directory on disk (required)
    cert_cache      Path to store Let's Encrypt certificates (required)
    listen_https    TLS server bind address (default :443)
    listen_http     HTTP server bind address (default :80)
    redirect_http   If true, redirects HTTP to HTTPS (default false)

    sites[]         Subdomain reverse proxy targets
      hostname        Subdomain that routes to this app (required)
      upstream_port   Port the child app listens on (required, 1-65535)
      binary_path     Path to the child app binary (optional, empty = proxy-only)

    api_routes[]    Path-based reverse proxy targets
      path            URL path prefix that routes to app (required)
      upstream_port   Port the child app listens on (required, 1-65535)
      strip_prefix    Strip the path prefix before forwarding (default false)

Config is validated on load and on SIGHUP reload. Validation checks:
- Required top-level fields (domain, static_root, cert_cache)
- Site hostnames are non-empty and unique
- Site upstream_port is 1-65535
- API route paths are non-empty and unique
- API route upstream_port is 1-65535

A failed reload leaves the previous config in effect — the gateway 
continues running without interruption.

## Deployment

### VPS Setup

Create a service user:

    sudo useradd -r -s /bin/bash -d /opt/leeforest leeforest
    sudo mkdir -p /opt/leeforest/{www,certs,apps}
    sudo chown -R leeforest:leeforest /opt/leeforest

Install systemd service:

    sudo cp deploy/leeforest.service /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable leeforest

For full initial server setup, see deploy/deployment.sh and 
gateway-operations.md.

### Push-to-Deploy

Create a bare Git repository on the VPS:

    sudo mkdir -p /opt/git
    sudo chown -R leeforest:leeforest /opt/git
    cd /opt/git
    git init --bare leeforest.git
    git symbolic-ref HEAD refs/heads/main

Add a post-receive hook at /opt/git/leeforest.git/hooks/post-receive:

    #!/bin/bash
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

Make it executable:

    chmod +x /opt/git/leeforest.git/hooks/post-receive

Add sudoers entry for passwordless service restart:

    leeforest ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart leeforest

Add the VPS as a remote and push:

    git remote add origin ssh://user@your-vps-ip/opt/git/leeforest.git
    git push origin main

### DNS Setup

Point A records to your VPS IP:

    @     A     YOUR_VPS_IP
    www   A     YOUR_VPS_IP

Add subdomain A records for child apps as needed:

    app   A     YOUR_VPS_IP

### Reloading Config Without Restart

After editing config.json on the VPS, reload without downtime:

    /opt/leeforest/scripts/reload-gateway.sh

This sends SIGHUP to the gateway, which reloads config, reconciles 
child apps, and updates routing. No restart required.

## Adding a Child App

1. Build a Go binary that listens on a localhost port:

       package main

       import (
           "fmt"
           "net/http"
       )

       func main() {
           http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
               fmt.Fprintf(w, "Hello from the app")
           })
           http.ListenAndServe("127.0.0.1:8081", nil)
       }

2. Copy the binary to the VPS:

       scp appname leeforest@your-vps-ip:/opt/leeforest/apps/appname/appname

3. Add a DNS A record for the subdomain on your registrar

4. Add a site entry to config.json on the VPS:

       {
         "hostname": "appname.yourdomain.dev",
         "upstream_port": 8081,
         "binary_path": "/opt/leeforest/apps/appname/appname"
       }

5. Reload the gateway:

       /opt/leeforest/scripts/reload-gateway.sh

The gateway will spawn the child app, add the subdomain to the Autocert 
whitelist, and route HTTPS traffic to it. No restart required — existing 
apps continue running.

On first HTTPS request, Autocert provisions a Let's Encrypt certificate 
automatically.

For full child app management including removal and git-based deployment, 
see docs/child-app.md.

## WebSocket Support

The reverse proxy handles WebSocket upgrades automatically. The gateway's 
IdleTimeout (120s) will close inactive connections — client-side heartbeat 
pings every 20-30s are recommended for long-lived connections.

## Documentation

| File | Description |
|---|---|
| gateway-structure.md | Architecture reference — codebase structure, module deep-dives, concurrency model |
| gateway-operations.md | Operations manual — deployment, config, troubleshooting, recovery |
| GATEWAY-QUICKREF.md | Quick reference cheat sheet for common commands |
| docs/child-app.md | Child app management guide — adding, removing, deploying apps |
| docs/local-build.md | Local building, testing, and deployment instructions |

## Security

- Gateway runs as a dedicated unprivileged user
- systemd hardening: ProtectSystem=strict, NoNewPrivileges, PrivateTmp, ProtectHome
- CAP_NET_BIND_SERVICE allows binding ports 80/443 without root
- Config validation rejects invalid configs before applying them
- Child apps listen on localhost only — not externally reachable
- All external traffic must pass through gateway (TLS termination + routing)

## License

MIT
