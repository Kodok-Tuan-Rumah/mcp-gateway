# Per-Key Rate Limiting Guide

> **Last Updated:** 2026-10-26  
> **Status:** Implemented

## Overview

Per-key rate limiting allows different API keys to have different rate limits based on their tier (free, pro, enterprise). This enables tiered service levels for different clients.

## Tiers

| Tier | RPM | Burst | Use Case |
|------|-----|-------|----------|
| **Free** | 30 | 5 | Development, testing |
| **Pro** | 120 | 20 | Production (standard) |
| **Enterprise** | 600 | 50 | High-volume production |

## Configuration

### Environment Variables

```bash
# Default tier for all keys (free/pro/enterprise)
# GATEWAY_RATE_LIMIT_RPM=60
# GATEWAY_RATE_LIMIT_BURST=10

# Per-key tier assignments (comma-separated "key:tier" pairs)
# GATEWAY_KEY_TIERS=sk-pro-123:pro,sk-ent-456:enterprise
```

### Runtime Tier Assignment

Tiers can also be set at runtime via the Go API:
```go
limiter.SetTier("sk-new-key", middleware.TierPro)
```

## Response Headers

When rate limited, the response includes:
- `X-RateLimit-Tier`: The tier name (e.g., "free", "pro", "enterprise")
- `Retry-After`: Seconds to wait before retrying

## Files

| File | Purpose |
|------|---------|
| `internal/middleware/rate_limit.go` | `APIKeyStore` with tiers, `PerKeyRateLimiter`, `TierConfig` |
| `internal/middleware/per_key_rate_limit_test.go` | 8 test cases |
| `internal/config/config.go` | `AuthConfig.KeyTiers`, env loading |
| `cmd/server/router.go` | Wire `PerKeyRateLimit` middleware |