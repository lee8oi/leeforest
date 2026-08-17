# Build for Linux (cross-compile from WSL or Windows)
make build

# Set YOUR_VPS_IP in Makefile first, then:
make deploy
make deploy-www
make deploy-config