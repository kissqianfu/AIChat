-- 0023：管理员 /g 选群会话（docs/01 §6.5.4）：私聊选群，群级指令作用于选定群。
CREATE TABLE IF NOT EXISTS admin_session (
  openid        TEXT PRIMARY KEY,
  current_group TEXT,
  updated_at    INTEGER NOT NULL
);
