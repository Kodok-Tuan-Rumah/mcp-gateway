# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go.mod and main.go
COPY go.mod .
COPY main.go .

# Download dependencies
RUN go mod tidy

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o mcp-stdio-adapter .

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/mcp-stdio-adapter .

# Add ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates

# Set entrypoint
ENTRYPOINT ["/app/mcp-stdio-adapter"]
