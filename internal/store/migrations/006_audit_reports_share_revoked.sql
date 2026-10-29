-- Additive: soft-revoke share tokens. Revoked tokens cannot be fetched via /r/{token}.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'audit_reports' AND column_name = 'share_revoked'
    ) THEN
        ALTER TABLE audit_reports ADD COLUMN share_revoked BOOLEAN NOT NULL DEFAULT FALSE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_audit_reports_share_revoked ON audit_reports (share_revoked);
