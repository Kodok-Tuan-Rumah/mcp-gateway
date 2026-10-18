.PHONY: check build vet test lint fmt demo

# mcp-gateway Makefile
#
# Editing tips: service lifecycle (up/down/status/logs) lives at the workspace
# root — `cd .. && make up | down | status | logs SERVICE=mcp-gateway`.
# Legacy per-service WSL targets (/mnt/d, rag :8003) were removed.

# --- Verification (run before every commit) ---
build:
	@go build ./...

vet:
	@go vet ./...

test:
	@go test -race -count=1 ./...

lint:
	@golangci-lint run --timeout=5m

fmt:
	@gofmt -l -w .

check: build vet test lint
	@echo "OK: build + vet + test -race + lint"

demo:
	@bash scripts/demo.sh
