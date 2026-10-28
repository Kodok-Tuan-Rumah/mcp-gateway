CREATE TABLE IF NOT EXISTS tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant_quotas (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    requests_per_minute INT NOT NULL DEFAULT 60,
    burst INT NOT NULL DEFAULT 10,
    PRIMARY KEY (tenant_id)
);

CREATE TABLE IF NOT EXISTS audit_trail (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID REFERENCES tenants(id),
    request_id TEXT NOT NULL,
    backend TEXT,
    decision TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
