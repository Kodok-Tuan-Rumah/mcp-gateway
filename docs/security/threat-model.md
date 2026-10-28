# Threat Model

- Tenant isolation: each API key maps to a tenant; data is scoped by tenant_id.
- Key rotation via /admin/rotate-key; revoke exposed keys immediately.
- Rate limiting per tenant tier.
