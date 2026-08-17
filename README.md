# leeforest.dev Gateway Server
A minimal Go-based web gateway with automatic Let's Encrypt TLS termination, 
static file serving, child app spawning, and reverse proxy routing.

Designed for self-hosted deployment on a single VPS with zero external 
dependencies beyond the Go binary and the standard library.

## Features

- Automatic Let's Encrypt certificates via golang.org/x/crypto/acme/autocert
- HTTP to HTTPS redirect on port 80
- Static file serving from disk (update content without rebuilding)
- Subdomain-based reverse proxy routing
- API path-based reverse proxy routing
- Child app spawning with automatic restart on crash
- Dynamic TLS whitelist (reads subdomains from config)
- systemd service with auto-restart
- Push-to-deploy via bare Git repository and post-receive hook

## Requirements

- Go 1.23 or newer
- A VPS with ports 80 and 443 open
- A domain name with DNS A records pointing to your VPS

## Quick Start

Clone the repo:

    git clone https://github.com/lee8oi/leeforest.git
    cd leeforest

Build the binary:

    go build -o leeforest ./cmd/gateway

Run locally (without TLS for development):

    ./leeforest -config config.json

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
    │   └── leeforest.service    # systemd unit file
    ├── config.json              # Runtime config (gitignored in production)
    ├── config.json.example      # Reference config for new users
    ├── go.mod
    ├── go.sum
    └── Makefile

## Configuration

Copy the example config and customize:

    cp config.json.example config.json

Config fields:

    domain          Primary domain name (added to TLS whitelist)
    static_root     Path to static file directory on disk
    cert_cache      Path to store Let's Encrypt certificates
    listen_https    TLS server bind address (default :443)
    listen_http     HTTP server bind address (default :80)
    redirect_http   If true, redirects HTTP to HTTPS (default true)

    sites[]         Subdomain reverse proxy targets
      hostname        Subdomain that routes to this app
      upstream_port   Port the child app listens on
      binary_path     Path to the child app binary (empty = no spawn)
      strip_prefix    Remove hostname prefix from path (default false)

    api_routes[]    Path-based reverse proxy targets
      path            URL path prefix that routes to app
      upstream_port   Port the child app listens on
      strip_prefix    Strip the path prefix before forwarding (default false)

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

3. Add a DNS A record for the subdomain on Namecheap (or your registrar)

4. Add a site entry to config.json on the VPS:

       {
         "hostname": "appname.yourdomain.dev",
         "upstream_port": 8081,
         "binary_path": "/opt/leeforest/apps/appname/appname"
       }

5. Restart the gateway:

       sudo systemctl restart leeforest

The gateway will spawn the child app on startup, route requests to it 
by subdomain, and obtain a TLS certificate automatically via Let's Encrypt.

## Security

- Gateway runs as a dedicated unprivileged user
- systemd hardening: ProtectSystem, NoNewPrivileges, PrivateTmp
- CAP_NET_BIND_SERVICE allows binding ports 80/443 without root
- Child apps inherit the same user context

## License

MIT