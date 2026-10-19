-- Durable document-ingestion jobs keep uploads out of the request path.
-- source_data is cleared when processing succeeds or fails.
CREATE TABLE IF NOT EXISTS ai_knowledge_ingestion_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    content_type VARCHAR(255) NOT NULL DEFAULT 'application/octet-stream',
    source_data BYTEA,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    extracted_items JSONB,
    error_message TEXT,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT chk_ai_knowledge_ingestion_status
        CHECK (status IN ('queued', 'processing', 'ready', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_ai_knowledge_ingestion_tenant_created
    ON ai_knowledge_ingestion_jobs (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ai_knowledge_ingestion_queue
    ON ai_knowledge_ingestion_jobs (status, created_at)
    WHERE status = 'queued';
