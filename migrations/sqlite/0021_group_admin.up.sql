-- 0021：群内本地管理员名单（M6-7 收尾 / R4-1 RBAC 修复）。
-- 权限只看本地表与 admin_user，不读平台 author.member_role（DEV_PROMPT §3.1）。
CREATE TABLE IF NOT EXISTS group_admin (
  group_id   TEXT NOT NULL,
  openid     TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY (group_id, openid)
);

CREATE INDEX IF NOT EXISTS idx_group_admin_group ON group_admin (group_id);
