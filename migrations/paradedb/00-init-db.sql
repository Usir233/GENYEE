-- ParadeDB bootstrap helper for GENYEE.
-- All relational application tables (tenants, models, knowledge_bases, sessions,
-- messages, etc.) are managed exclusively by migrations/versioned/ (000000..000140).
-- This script only provisions required ParadeDB / pgvector extensions, guards the
-- tenants_id_seq floor when already present, and idempotently initializes the
-- ParadeDB embeddings table + BM25 / HNSW indexes.

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS pg_search;

-- Set the starting value for tenants id sequence when the sequence already exists
-- and its current value is below 10000.
DO $$
DECLARE
    current_val BIGINT;
BEGIN
    SELECT last_value INTO current_val FROM tenants_id_seq;
    IF current_val < 10000 THEN
        ALTER SEQUENCE tenants_id_seq RESTART WITH 10000;
    END IF;
EXCEPTION
    WHEN undefined_table THEN
        NULL;
END $$;

CREATE TABLE IF NOT EXISTS embeddings (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    source_id VARCHAR(64) NOT NULL,
    source_type INTEGER NOT NULL,
    chunk_id VARCHAR(64),
    knowledge_id VARCHAR(64),
    knowledge_base_id VARCHAR(64),
    content TEXT,
    dimension INTEGER NOT NULL,
    embedding halfvec,
    is_enabled BOOLEAN DEFAULT TRUE,
    tag_id VARCHAR(36)
);

CREATE UNIQUE INDEX IF NOT EXISTS embeddings_unique_source ON embeddings(source_id, source_type);
CREATE INDEX IF NOT EXISTS idx_embeddings_is_enabled ON embeddings(is_enabled);
CREATE INDEX IF NOT EXISTS idx_embeddings_knowledge_base_id ON embeddings(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_embeddings_knowledge_id ON embeddings(knowledge_id);
CREATE INDEX IF NOT EXISTS idx_embeddings_chunk_id ON embeddings(chunk_id);
CREATE INDEX IF NOT EXISTS idx_embeddings_tag_id ON embeddings(tag_id);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'embeddings_search_idx') THEN
        CREATE INDEX embeddings_search_idx ON embeddings
        USING bm25 (id, knowledge_base_id, content, knowledge_id, chunk_id)
        WITH (
            key_field = 'id',
            text_fields = '{
                "content": {
                  "tokenizer": {"type": "chinese_lindera"}
                }
            }'
        );
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS embeddings_embedding_idx_3584
    ON embeddings USING hnsw ((embedding::halfvec(3584)) halfvec_cosine_ops)
    WITH (m = 16, ef_construction = 64)
    WHERE (dimension = 3584);
CREATE INDEX IF NOT EXISTS embeddings_embedding_idx_798
    ON embeddings USING hnsw ((embedding::halfvec(798)) halfvec_cosine_ops)
    WITH (m = 16, ef_construction = 64)
    WHERE (dimension = 798);
CREATE INDEX IF NOT EXISTS embeddings_embedding_idx_1024
    ON embeddings USING hnsw ((embedding::halfvec(1024)) halfvec_cosine_ops)
    WITH (m = 16, ef_construction = 64)
    WHERE (dimension = 1024);
