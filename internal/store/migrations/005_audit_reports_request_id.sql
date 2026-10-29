-- Additive: make audit_reports and audit_report_claims join-able via request_id.
-- request_id propagates the gateway trace through to audit reports so a report
-- and its claims can be traced back to the originating gateway request.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'audit_reports' AND column_name = 'request_id'
    ) THEN
        ALTER TABLE audit_reports ADD COLUMN request_id TEXT;
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'audit_report_claims' AND column_name = 'request_id'
    ) THEN
        ALTER TABLE audit_report_claims ADD COLUMN request_id TEXT;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_audit_reports_request_id ON audit_reports (request_id);
CREATE INDEX IF NOT EXISTS idx_audit_report_claims_request_id ON audit_report_claims (request_id);
