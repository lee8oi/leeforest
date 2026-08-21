# Leeforest Gateway — Quick Reference

> One-page cheat sheet. For full details see GATEWAY-ARCHITECTURE.md and GATEWAY-OPERATIONS.md.

## VPS Access

    ssh leeforestvps

- Host: server1.leeforest.dev (66.29.133.233)
- Debian 12, user: leeforest
- Key-based SSH only, root disabled

## File Paths

    /opt/leeforest/leeforest          # Gateway binary
    /opt/leeforest/leeforest.pid      # PID file
    /opt/leeforest/config.json         # Config (NOT in git)
    /opt/leeforest/certs/             # TLS cert cache
    /opt/leeforest/www/               # Static files
    /opt/leeforest/apps/<appname>/     # Child app binaries
    /opt/leeforest/scripts/reload-gateway.sh
    /opt/git/leeforest.git            # Gateway bare repo
    /opt/git/<appname>.git            # Child app bare repos
    /etc/systemd/system/leeforest.service

## Common Commands

**Deploy gateway code:**

    git push origin main

**Reload gateway config (no restart):**

    /opt/leeforest/scripts/reload-gateway.sh

**Full restart (use sparingly):**

    sudo systemctl restart leeforest

**Check logs:**

    sudo journalctl -u leeforest -n 20 --no-pager
    sudo journalctl -u leeforest -f                    # follow

**Check running child apps:**

    sudo journalctl -u leeforest -n 20 --no-pager | grep "Started app"

**Edit config:**

    nano /opt/leeforest/config.json

**Reload systemd after service file changes:**

    sudo systemctl daemon-reload

**Test child app locally:**

    curl http://127.0.0.1:<PORT>

**Test through gateway:**

    curl https://<subdomain>.leeforest.dev

**Cross-compile locally:**

    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o leeforest ./cmd/gateway

**Make targets:**

    make build          # Cross-compile to bin/
    make deploy         # Build + SCP + restart
    make deploy-www     # Rsync static files
    make deploy-config  # SCP config + restart

## Port Allocations

    8081  status       (active)
    8082  (available)
    8084  (skip — was temporary testapp)
    8085+ available

## Adding a Child App

1. Build Go binary listening on `127.0.0.1:<PORT>`
2. Copy to `/opt/leeforest/apps/<appname>/<appname>`
3. Add DNS A record on Namecheap: `<subdomain>` → `66.29.133.233`
4. Add site entry to `/opt/leeforest/config.json`
5. Run `/opt/leeforest/scripts/reload-gateway.sh`
6. Verify: `curl https://<subdomain>.leeforest.dev`

Config entry template:

    {
      "hostname": "<subdomain>.leeforest.dev",
      "upstream_port": <PORT>,
      "binary_path": "/opt/leeforest/apps/<appname>/<appname>",
      "strip_prefix": false
    }

## Removing a Child App

1. Remove site entry from `/opt/leeforest/config.json`
2. Run `/opt/leeforest/scripts/reload-gateway.sh`
3. Optional: `rm -rf /opt/leeforest/apps/<appname>` and remove DNS record

## API Route Config (path-based proxying)

Add to `api_routes` array in config:

    {
      "path": "/api/<appname>/",
      "upstream_port": <PORT>,
      "strip_prefix": true
    }

## WebSocket Notes

- Supported via reverse proxy (automatic upgrade)
- IdleTimeout: 120s — client must send heartbeat ping every 20-30s
- WriteTimeout: 10s — may need per-handler deadline clearing for stateful connections

## Key Warnings

- `config.json` is NOT in git — do not commit it
- Repo `leeforest.service` may not match VPS copy (ReadWritePaths discrepancy)
- Gateway deploy requires full restart; child app deploy uses SIGHUP (zero-downtime)
- Autocert provisions certs on first HTTPS request to new subdomain (takes a few seconds)
