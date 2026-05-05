.PHONY: build ui-build dev clean windows deploy

# Build the Svelte frontend
ui-build:
	cd ui && npm ci && npm run build

# Build the Go binary (depends on frontend)
build: ui-build
	CGO_ENABLED=0 go build -o longbox ./cmd/longbox

# Development: just build Go (assumes frontend is built separately)
dev:
	go run ./cmd/longbox --config config.yaml

# Clean build artifacts
clean:
	rm -f longbox
	rm -rf ui/build ui/.svelte-kit

# Cross-compile releases
release: ui-build
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/longbox-linux-amd64 ./cmd/longbox
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/longbox-darwin-arm64 ./cmd/longbox
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o dist/longbox-windows-amd64.exe ./cmd/longbox

# Build the Windows binary in-tree (./longbox.exe). Used by `make deploy`.
windows: ui-build
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o longbox.exe ./cmd/longbox

# Build + push to the running server at 192.168.1.163. The SMB share E:\
# must already be mounted at /Volumes/192.168.1.163. See
# scripts/deploy-server.sh for the full sequence and exit codes; auto
# relaunch on the server requires scripts/longbox-run.bat to be the
# launcher in use.
deploy: windows
	bash scripts/deploy-server.sh
