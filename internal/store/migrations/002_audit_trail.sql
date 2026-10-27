CREATE TABLE IF NOT EXISTS audit_trail (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id              TEXT,
    backend                 TEXT,
    claim                   TEXT,
    decision                TEXT,
    calibrated_confidence   DOUBLE PRECISION,
    calibrated_decision     TEXT,
    evidence                JSONB,
    span_level_attribution  JSONB,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_trail_request_id ON audit_trail (request_id);
CREATE INDEX IF NOT EXISTS idx_audit_trail_created_at ON audit_trail (created_at);
