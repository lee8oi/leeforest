# Child App Management

This document describes how to add and remove child apps from the leeforest.dev gateway.

## Overview

Child apps are standalone Go binaries that listen on a localhost port. The gateway handles TLS termination, subdomain routing, and process supervision. Apps can be added or removed at runtime without restarting the gateway.

## Adding a Child App

### 1. Build the App

Create a Go binary that listens on 127.0.0.1:<PORT> using plain HTTP. No TLS handling is needed — the gateway proxies HTTPS traffic to the app over localhost.

Cross-compile if building on a different OS:

    GOOS=linux GOARCH=amd64 go build -o <appname> .

### 2. Deploy the Binary

Copy the compiled binary to the VPS:

    scp <appname> leeforestvps:/opt/leeforest/apps/<appname>/<appname>

Alternatively, use a bare git repo with a post-receive hook that builds and deploys automatically (see existing app repos under /opt/git/).

### 3. Add DNS Record

On Namecheap, add an A record for the subdomain:

- Type: A
- Host: <subdomain>
- Value: 66.29.133.233
- TTL: Automatic

### 4. Add Config Entry

Edit the gateway config on the VPS:

    nano /opt/leeforest/config.json

Add a new entry to the sites array:

    {
      "hostname": "<subdomain>.leeforest.dev",
      "upstream_port": <PORT>,
      "binary_path": "/opt/leeforest/apps/<appname>/<appname>"
    }

Port assignments should avoid conflicts. Current allocations:

- 8081: status

### 5. Reload Gateway

    /opt/leeforest/scripts/reload-gateway.sh

The gateway will:

- Reload config.json
- Spawn the new child app
- Add the subdomain to the Autocert whitelist
- Route HTTPS traffic to the app via reverse proxy

No gateway restart required. Existing apps continue running.

### 6. Verify

Check the gateway logs:

    sudo journalctl -u leeforest -n 10 --no-pager

Look for:

- Started app: /opt/leeforest/apps/<appname>/<appname> (PID xxxxx)
- The subdomain listed under "Active sites"

Test locally:

    curl http://127.0.0.1:<PORT>

Test through the gateway:

    curl https://<subdomain>.leeforest.dev

On first HTTPS request, Autocert provisions a Let's Encrypt certificate. This may take a few seconds.

## Removing a Child App

### 1. Remove Config Entry

Edit the gateway config on the VPS:

    nano /opt/leeforest/config.json

Remove the site entry from the sites array. Save and exit.

### 2. Reload Gateway

    /opt/leeforest/scripts/reload-gateway.sh

The gateway will:

- Reload config.json
- Gracefully stop the removed child app
- Remove the subdomain from routing and TLS whitelist
- Leave all other apps running

### 3. Verify

Check the gateway logs:

    sudo journalctl -u leeforest -n 10 --no-pager

Look for:

- Stopping app: /opt/leeforest/apps/<appname>/<appname> (PID xxxxx)
- App ... stopped during shutdown
- The subdomain no longer listed under "Active sites"

### 4. Optional Cleanup

Remove the binary and source files if no longer needed:

    rm -rf /opt/leeforest/apps/<appname>

Remove the DNS record on Namecheap if the subdomain will not be reused.

## Post-Receive Hook Template

For apps deployed via bare git repos, use this post-receive hook. Replace APP_NAME with the app name:

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

## Notes

- The gateway runs as the leeforest user. No sudo is required for app management or gateway reloads.
- Child apps inherit stdout/stderr from the gateway and appear in journalctl output.
- If a child app crashes, the gateway automatically restarts it with exponential backoff (1s, 2s, 4s, ... up to 30s).
- The gateway PID file is located at /opt/leeforest/leeforest.pid.
- Config changes take effect only after sending SIGHUP (via the reload script or kill command).
- WebSocket connections are supported through the reverse proxy. Use client-side heartbeat pings (20-30s) to prevent gateway idle timeout (120s) from closing the connection.
