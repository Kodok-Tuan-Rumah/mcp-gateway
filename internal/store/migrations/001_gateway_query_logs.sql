-- Tabel milik mcp-gateway.
--
-- Dipisah dari query_logs karena tabel itu dimiliki arxiv-research-assistant
-- dengan semantik berbeda: go-backend mengisi retrieval_ms/generation_ms sebagai
-- latensi milestone backend dan model sebagai nama model LLM, sedangkan gateway
-- butuh orchestrator, status evaluasi, dan latensi total request. Menulis dua
-- definisi itu ke kolom yang sama membuat query analitis tidak bisa dipercaya.
--
-- Idempoten: aman dijalankan berulang kali.

CREATE TABLE IF NOT EXISTS gateway_query_logs (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),

    request_id         TEXT,
    query              TEXT        NOT NULL,
    answer             TEXT,

    backend            TEXT,
    backend_model      TEXT,
    orchestrator       TEXT,   -- rule-based planner

    decision           TEXT,   -- answer | abstain | error
    failure_type       TEXT,   -- none | backend_error | ...

    evaluation_status  TEXT,   -- completed | unavailable | skipped
    faithfulness       DOUBLE PRECISION,  -- NULL saat belum/skipped, bukan 0
    failure_mode       TEXT,

    duration_ms        INTEGER,  -- latensi total request, diukur gateway

    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Analisis yang paling sering dipakai: tren skor per status evaluasi.
CREATE INDEX IF NOT EXISTS idx_gateway_query_logs_created_at
    ON gateway_query_logs (created_at);

-- Membantu memisahkan "sudah dinilai" dari "belum dinilai" tanpa membaca NULL.
CREATE INDEX IF NOT EXISTS idx_gateway_query_logs_eval_status
    ON gateway_query_logs (evaluation_status, created_at);
