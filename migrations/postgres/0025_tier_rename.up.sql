-- 0025：档位更名对齐 docs/09 §3.1 —— free→full、quiet→night（work/maint 不变）。
-- 存量行原值转换；ParseTier 对未知值报错，不迁移会导致启动后 override/例外日静默失效。
UPDATE schedule_override  SET tier = 'full'  WHERE tier = 'free';
UPDATE schedule_override  SET tier = 'night' WHERE tier = 'quiet';
UPDATE schedule_exception SET tier = 'full'  WHERE tier = 'free';
UPDATE schedule_exception SET tier = 'night' WHERE tier = 'quiet';
