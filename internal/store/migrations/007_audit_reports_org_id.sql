-- Additive: org_id scoping for audit_reports (multi-tenancy foundation, P1).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'audit_reports' AND column_name = 'org_id'
    ) THEN
        ALTER TABLE audit_reports ADD COLUMN org_id TEXT;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_audit_reports_org_id ON audit_reports (org_id);