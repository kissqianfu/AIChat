-- 0005：情绪状态（docs/01 §5）。单人格下 baseline 由 persona.yaml 提供，表里存当前值。
CREATE TABLE IF NOT EXISTS emotion_state (
  owner_id    TEXT NOT NULL,
  scene       TEXT NOT NULL,
  joy         REAL NOT NULL DEFAULT 0,
  anger       REAL NOT NULL DEFAULT 0,
  sorrow      REAL NOT NULL DEFAULT 0,
  fear        REAL NOT NULL DEFAULT 0,
  love        REAL NOT NULL DEFAULT 0,
  disgust     REAL NOT NULL DEFAULT 0,
  desire      REAL NOT NULL DEFAULT 0,
  safety      REAL NOT NULL DEFAULT 0,
  curiosity   REAL NOT NULL DEFAULT 0,
  expression  REAL NOT NULL DEFAULT 0,
  approval    REAL NOT NULL DEFAULT 0,
  control     REAL NOT NULL DEFAULT 0,
  connection  REAL NOT NULL DEFAULT 0,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (owner_id, scene)
);
