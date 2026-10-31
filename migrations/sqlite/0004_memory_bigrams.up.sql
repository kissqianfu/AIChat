-- 0004：记忆 bigram 串（SQL 粗筛用；避免依赖 FTS5/pg_trgm，跨驱动一致）。
ALTER TABLE memory_item ADD COLUMN bigrams TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_memory_subject ON memory_item (subject, predicate);
