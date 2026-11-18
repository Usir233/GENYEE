-- Migration: 000061_wiki_page_hierarchy (rollback)
-- wiki_folders and wiki_pages hierarchy columns were introduced in 000037_wiki_and_indexing;
-- only drop the standalone folder_id index added by 000061.
DROP INDEX IF EXISTS idx_wiki_pages_folder_id;
