-- Migration 000058 DOWN: knowledges.source was already VARCHAR(2048) in 000000_init.
ALTER TABLE knowledges
    ALTER COLUMN source TYPE VARCHAR(2048) USING LEFT(source, 2048);
