.PHONY: all clean lsp-server lsp-server-wasm escalier escalier-wasm npm-binaries

# Default target
all: lsp-server escalier

lsp-server:
	go build -o ./bin/lsp-server ./cmd/lsp-server

lsp-server-wasm:
	GOOS=js GOARCH=wasm go build -o ./bin/lsp-server.wasm ./cmd/lsp-server

escalier:
	go build -o ./bin/escalier ./cmd/escalier

escalier-wasm:
	GOOS=js GOARCH=wasm go build -o ./bin/escalier.wasm ./cmd/escalier

all-targets: lsp-server lsp-server-wasm escalier escalier-wasm

# Cross-compiles escalier into the @escalier/cli-<platform>-<arch> packages
# and lsp-server into bin/<platform>-<arch>/ for every published target.
npm-binaries:
	node tools/build-binaries.mjs

# Clean build artifacts
clean:
	rm -f ./bin/lsp-server ./bin/lsp-server.wasm ./bin/escalier ./bin/escalier.wasm
