.PHONY: build ui-build dev clean

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
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/longbox-windows-amd64.exe ./cmd/longbox
