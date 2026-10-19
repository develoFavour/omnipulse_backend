-- Processed ingestion jobs release their temporary upload bytes.
ALTER TABLE ai_knowledge_ingestion_jobs
    ALTER COLUMN source_data DROP NOT NULL;
