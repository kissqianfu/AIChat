-- 0002：message 加 sender_name（上下文装配要"带发言人标签"的群公共层）。
ALTER TABLE message ADD COLUMN sender_name TEXT;
