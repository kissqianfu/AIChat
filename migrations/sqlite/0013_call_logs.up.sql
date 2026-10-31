-- 0013：LLM / 工具调用日志（M7-9）。
CREATE TABLE IF NOT EXISTS llm_call_log (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id          TEXT NOT NULL DEFAULT '',
  slot              TEXT NOT NULL DEFAULT '',
  prompt_tokens     INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  finish_reason     TEXT NOT NULL DEFAULT '',
  created_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_llm_log_created ON llm_call_log (created_at DESC);

CREATE TABLE IF NOT EXISTS tool_call_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id   TEXT NOT NULL DEFAULT '',
  name       TEXT NOT NULL,
  is_error   INTEGER NOT NULL DEFAULT 0,
  chars      INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tool_log_created ON tool_call_log (created_at DESC);
