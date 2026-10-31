-- 0014：LLM 成本估算（M7-9；micros，价格常量在代码里）。
ALTER TABLE llm_call_log ADD COLUMN cost_micros BIGINT NOT NULL DEFAULT 0;
