-- Audit reports for Vertical 1 dossier (additive; does not alter existing tables).
CREATE TABLE IF NOT EXISTS audit_reports (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    api_key_tier    TEXT,
    status          TEXT        NOT NULL DEFAULT 'completed',
    claim_count     INTEGER     NOT NULL DEFAULT 0,
    summary         JSONB,
    share_token     TEXT        UNIQUE,
    share_expires_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS audit_report_claims (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id       UUID        NOT NULL REFERENCES audit_reports(id) ON DELETE CASCADE,
    claim_index     INTEGER     NOT NULL,
    claim           TEXT        NOT NULL,
    decision        TEXT,
    confidence_zone TEXT,
    calibrated_confidence DOUBLE PRECISION,
    failure_type    TEXT,
    evidence        JSONB,
    span_attribution JSONB,
    evaluation      JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_report_claims_report_id ON audit_report_claims (report_id);
CREATE INDEX IF NOT EXISTS idx_audit_reports_share_token ON audit_reports (share_token);
