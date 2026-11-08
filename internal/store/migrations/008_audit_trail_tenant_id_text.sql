-- Fix: audit_trail.tenant_id was UUID REFERENCES tenants(id), but no code
-- inserts into tenants table, causing FK violation on every LogAudit call.
-- Change to TEXT without FK so audit writes succeed regardless of tenant.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'audit_trail' AND column_name = 'tenant_id'
          AND data_type = 'uuid'
    ) THEN
        ALTER TABLE audit_trail ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
    END IF;
END $$;

-- Drop the FK constraint if it exists (from earlier migration).
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'audit_trail' AND constraint_type = 'FOREIGN KEY'
          AND constraint_name LIKE '%tenant_id%'
    ) THEN
        ALTER TABLE audit_trail DROP CONSTRAINT IF EXISTS audit_trail_tenant_id_fkey;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_audit_trail_tenant_id ON audit_trail (tenant_id);
