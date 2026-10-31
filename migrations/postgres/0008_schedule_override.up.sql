-- 0008：分时段临时强制档（/hours now；docs/09 §9）。
-- key = 'global' 或群 openid；群级优先于全局；expires_at 到点自动失效（由 Scheduler 判定）。
-- 档位：full / work / night / maint（0025 起由 free/quiet 更名）；maint 只能通过本表进入。
CREATE TABLE IF NOT EXISTS schedule_override (
  key        TEXT PRIMARY KEY,
  tier       TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sched_override_expire ON schedule_override (expires_at);
