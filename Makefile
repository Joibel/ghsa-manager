# Adapted from todaydeck's Makefile. Run inside the nix dev shell
# (direnv or `nix develop`), which provides every tool.

.PHONY: all build test lint fmt tidy gitleaks vuln go-pin-check go-toolchain-sync check

all: lint test build

build:
	go build -o bin/ghsa-manager .

test:
	go test -race ./...

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

tidy:
	go mod tidy

gitleaks:
	gitleaks git --redact --no-banner .

vuln:
	govulncheck ./...

go-pin-check:
	@shell_ver=$$(GOTOOLCHAIN=local go env GOVERSION | sed 's/^go//'); \
	mod_ver=$$(sed -n 's/^go \([0-9][0-9.]*\)$$/\1/p' go.mod | head -1); \
	if [ "$$shell_ver" != "$$mod_ver" ]; then \
		echo "go.mod says go $$mod_ver but the toolchain is go $$shell_ver."; \
		exit 1; \
	fi; \
	echo "go pin coherent: toolchain and go.mod both go$$shell_ver"

go-toolchain-sync:
	./scripts/go-toolchain-sync.sh

check: go-pin-check lint test gitleaks vuln
