-- 0027：拆掉估算计费的最后一处。cost_micros 自 0014 起按写死的常量
-- （$0.14/M prompt、$0.28/M completion）在写入时估算，全仓库只写不读；
-- 2026-11-01 起进程内不做任何估算，花费只看官方余额，此列随之删除。
ALTER TABLE llm_call_log DROP COLUMN cost_micros;
