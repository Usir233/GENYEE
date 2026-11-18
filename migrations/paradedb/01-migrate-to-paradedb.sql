-- 迁移脚本：存量原生 PostgreSQL 切换至 ParadeDB 扩展与检索索引
-- 注意：执行前请先备份数据库，并使用 paradedb/paradedb:v0.25.11-pg17 镜像启动实例。
--
-- 1. 在原 PostgreSQL 实例导出数据：
--    pg_dump -U postgres -h localhost -p 5432 -d your_database > backup.sql
--
-- 2. 在目标 ParadeDB 实例导入数据：
--    psql -U postgres -h localhost -p 5432 -d your_database < backup.sql
--
-- 3. 启用 ParadeDB 全文与向量扩展并补齐 BM25 / HNSW 索引：

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS pg_search;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'embeddings') THEN
        IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'embeddings_search_idx') THEN
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
    END IF;
END $$;

-- 4. 只读校验扩展版本（不会污染业务表结构）
SELECT extname, extversion
FROM pg_extension
WHERE extname IN ('uuid-ossp', 'vector', 'pg_trgm', 'pg_search')
ORDER BY extname;
