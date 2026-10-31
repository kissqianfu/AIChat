-- 0016：群级绘图开关（M7-3）：默认关，群管理用 /draw on 开启。
ALTER TABLE group_config ADD COLUMN draw_allow BOOLEAN NOT NULL DEFAULT false;
