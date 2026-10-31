-- 0020：群级自主插话开关（M6-7 /interject on|off）：默认关，需同时打开发送侧 interject.enabled。
ALTER TABLE group_config ADD COLUMN interject_allow BOOLEAN NOT NULL DEFAULT false;
