# API Key Rotation Guide

> **Last Updated:** 2026-10-26  
> **Status:** Implemented

## Overview

API key rotation allows updating valid API keys without restarting the gateway service. This is critical for security incidents (key compromise) and key lifecycle management.

## Endpoints

### Rotate Keys

```bash
POST /admin/rotate-key
Content-Type: application/json

{
  "new_keys": ["sk-live-new1", "sk-live-new2"],
  "admin_key": "your-admin-key"
}
```

**Response:**
```json
{
  "status": "success",
  "message": "API keys rotated successfully",
  "count": 2
}
```

### Check Key Status

```bash
GET /admin/key-status
```

**Response:**
```json
{
  "key_count": 2,
  "keys": ["sk-l****ew1", "sk-l****ew2"]
}
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `GATEWAY_API_KEYS` | (empty) | Comma-separated initial API keys |
| `GATEWAY_ADMIN_KEY` | (empty) | Admin key for rotation authorization. Empty = no protection |

## Usage Examples

### Rotate keys (no admin protection)
```bash
curl -X POST http://localhost:8090/admin/rotate-key \
  -H "Content-Type: application/json" \
  -d '{"new_keys":["sk-live-new1","sk-live-new2"]}'
```

### Rotate keys (with admin protection)
```bash
curl -X POST http://localhost:8090/admin/rotate-key \
  -H "Content-Type: application/json" \
  -d '{"new_keys":["sk-live-new1"],"admin_key":"your-admin-key"}'
```

### Check current keys
```bash
curl http://localhost:8090/admin/key-status
```

## Security Notes

- Admin key is optional. If `GATEWAY_ADMIN_KEY` is empty, anyone can rotate keys.
- For production, always set `GATEWAY_ADMIN_KEY`.
- Old keys are immediately invalidated after rotation.
- Keys are masked in status endpoint (first 4 + last 4 chars visible).

## Files

| File | Purpose |
|------|---------|
| `internal/admin/admin.go` | Rotation handler, key status, masking |
| `internal/admin/admin_test.go` | 7 test cases |
| `internal/middleware/rate_limit.go` | `APIKeyStore` with `Replace()`, `Add()`, `Remove()` |
| `internal/config/config.go` | `AuthConfig.AdminKey` field |
| `cmd/server/router.go` | Admin endpoints wired |