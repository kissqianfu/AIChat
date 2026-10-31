-- 0024：例外日（节假日 / 调休 / 年会 / 临时静音），docs/09 §4.4。
-- 群级优先于全局；区间在写入时展开成逐条记录，不存区间。
-- 只人工维护（/hours holiday），不接第三方 API。tier：full / work / night / maint（0025 起由 free/quiet 更名）。
CREATE TABLE IF NOT EXISTS schedule_exception (
  ex_date    DATE NOT NULL,
  scope      TEXT NOT NULL DEFAULT 'global',
  scope_id   TEXT NOT NULL DEFAULT '',
  tier       TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (ex_date, scope, scope_id)
);
CREATE INDEX IF NOT EXISTS idx_sched_exc_date ON schedule_exception (ex_date);
