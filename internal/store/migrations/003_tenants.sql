-- Tenants and quotas (multi-tenant foundation).
-- NOTE (P2-9): These tables are created for future multi-tenancy support but
-- are NOT yet referenced by any Go code. Tenant identity is currently managed
-- in-memory via APIKeyStore.orgs (key -> tenant_id mapping). The audit_trail
-- tenant_id column below IS written by LogAudit. The tenants and tenant_quotas
-- tables are reserved for future use; do not remove from this migration as
-- existing databases may already have them.
-- audit_trail is created by 002_audit_trail.sql; this migration guarantees the
-- tenant_id column exists on it via an idempotent, versioned ALTER TABLE
-- (never CREATE TABLE IF NOT EXISTS on an already-existing table).
CREATE TABLE IF NOT EXISTS tenants (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant_quotas (
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    requests_per_minute INT NOT NULL DEFAULT 60,
    burst             INT NOT NULL DEFAULT 10,
    PRIMARY KEY (tenant_id)
);

-- Ensure audit_trail has tenant_id. Idempotent: safe to re-run.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'audit_trail' AND column_name = 'tenant_id'
    ) THEN
        ALTER TABLE audit_trail ADD COLUMN tenant_id TEXT;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_audit_trail_tenant_id ON audit_trail (tenant_id);
