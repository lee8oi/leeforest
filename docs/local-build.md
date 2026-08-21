# Local Build & Testing Guide

## Prerequisites

- Go 1.26+ installed and on PATH
- Git (with SSH keys configured for GitHub)
- Optional: WSL on Windows (recommended for cross-compiling to Linux)

## Clone the Repository

    git clone git@github.com:lee8oi/leeforest.git
    cd leeforest

## Build for Linux (Cross-Compile)

If testing on the VPS or building for deployment:

    make build

Output binary lands at `bin/leeforest`. The Makefile sets `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` automatically.

## Build for Local OS

If you want to run the gateway on your local machine (e.g., for testing without a VPS):

    go build -o bin/leeforest ./cmd/gateway

## Run Locally

Create a `config.json` in the project root (this file is gitignored, so it won't be committed):

    {
      "domain": "localhost",
      "static_root": "./www",
      "cert_cache": "./certs",
      "listen_https": ":4443",
      "listen_http": ":8080",
      "redirect_http": true,
      "sites": [],
      "api_routes": []
    }

Then run:

    make run

Or manually:

    ./bin/leeforest -config config.json

Notes for local testing:
- Use higher ports (e.g., 4443, 8080) to avoid needing root or CAP_NET_BIND_SERVICE
- Autocert won't provision real certificates for `localhost` — HTTPS will fail unless you provide your own certs or disable TLS for testing
- With no sites configured, the gateway serves static files from `./www` and is useful for testing the static file server and routing
- To test child app proxying, run a child app on a localhost port and add a site entry pointing to it

## Deploy to VPS

If the VPS IP is already set in the Makefile:

    make deploy          # Build, SCP binary, restart service
    make deploy-www      # Rsync static files to VPS
    make deploy-config   # SCP config.json to VPS, restart service

Alternatively, just push to main and let the post-receive hook handle it:

    git push origin main

## Clean Up

    make clean

Removes the `bin/` directory.
