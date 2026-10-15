.PHONY: check build vet test lint fmt demo start stop status logs

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

start:
	@echo "Starting all services..."
	@cd /mnt/d/arxiv-research-assistant && docker compose -f infra/docker-compose.yml up -d
	@sleep 3
	@echo "Starting Go backend..."
	@cd /mnt/d/arxiv-research-assistant/go-backend && nohup bash -c 'go run cmd/server/main.go > /tmp/go-backend.log 2>&1' &
	@echo "Starting ML service..."
	@cd /mnt/d/arxiv-research-assistant/python-ml && nohup bash -c '. venv/bin/activate && uvicorn main:app --host 0.0.0.0 --port 8001 > /tmp/ml-service.log 2>&1' &
	@echo "Starting eval service..."
	@cd /mnt/d/llm-eval-framework && nohup bash -c '. venv/bin/activate && uvicorn api:app --host 0.0.0.0 --port 8002 > /tmp/eval-service.log 2>&1' &
	@echo "Starting retrieval service..."
	@cd /mnt/d/rag-research && nohup bash -c '. venv/bin/activate && TRANSFORMERS_OFFLINE=1 uvicorn api:app --host 0.0.0.0 --port 8003 > /tmp/rag-service.log 2>&1' &
	@echo "Building MCP Gateway..."
	@cd /mnt/d/mcp-gateway && go build -o /tmp/mcp-gateway-bin ./cmd/server/ && nohup bash -c 'set -a; . /mnt/d/mcp-gateway/.env; set +a; /tmp/mcp-gateway-bin > /tmp/mcp-gateway.log 2>&1' &
	@sleep 30
	@$(MAKE) status

stop:
	@echo "Stopping all services..."
	@pkill -f "uvicorn" || true
	@pkill -f "go run cmd/server" || true
	@pkill -f "go-build" || true
	@pkill -f "mcp-gateway-bin" || true
	@fuser -k 8080/tcp || true
	@fuser -k 8001/tcp || true
	@fuser -k 8002/tcp || true
	@fuser -k 8003/tcp || true
	@fuser -k 8090/tcp || true
	@cd /mnt/d/arxiv-research-assistant && docker compose -f infra/docker-compose.yml down
	@echo "All services stopped"

status:
	@echo "=== Service Status ==="
	@curl -s http://127.0.0.1:8080/health | python3 -m json.tool 2>/dev/null && echo "Go backend     :8080 ✅" || echo "Go backend     :8080 ❌"
	@curl -s http://127.0.0.1:8001/health > /dev/null 2>&1 && echo "ML service     :8001 ✅" || echo "ML service     :8001 ❌"
	@curl -s http://127.0.0.1:8002/health > /dev/null 2>&1 && echo "Eval service   :8002 ✅" || echo "Eval service   :8002 ❌"
	@curl -s http://127.0.0.1:8003/health > /dev/null 2>&1 && echo "Retrieval svc  :8003 ✅" || echo "Retrieval svc  :8003 ❌"
	@curl -s http://127.0.0.1:8090/health > /dev/null 2>&1 && echo "MCP Gateway    :8090 ✅" || echo "MCP Gateway    :8090 ❌"

logs:
	@echo "=== Go Backend ===" && tail -20 /tmp/go-backend.log 2>/dev/null || echo "(no log)"
	@echo "=== ML Service ===" && tail -20 /tmp/ml-service.log 2>/dev/null || echo "(no log)"
	@echo "=== Eval Service ===" && tail -20 /tmp/eval-service.log 2>/dev/null || echo "(no log)"
	@echo "=== RAG Service ===" && tail -20 /tmp/rag-service.log 2>/dev/null || echo "(no log)"
	@echo "=== MCP Gateway ===" && tail -20 /tmp/mcp-gateway.log 2>/dev/null || echo "(no log)"
