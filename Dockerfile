FROM golang:1.22.12-alpine@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o mcp-gateway ./cmd/server/

FROM alpine:3.19@sha256:6baf43584bcb78f2e5847d1de515f23499913ac9f12bdf834811a3145eb11ca1
WORKDIR /app
COPY --from=builder /app/mcp-gateway .
EXPOSE 8090
CMD ["./mcp-gateway"]
