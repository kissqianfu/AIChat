package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/storage/db"
)

// CallLogRepo 写 LLM/工具调用日志（M7-9）。
type CallLogRepo struct {
	store *db.Store
	clk   clock.Clock
}

// NewCallLogRepo 构造调用日志仓储。
func NewCallLogRepo(store *db.Store, clk clock.Clock) *CallLogRepo {
	if clk == nil {
		clk = clock.System()
	}
	return &CallLogRepo{store: store, clk: clk}
}

// InsertLLM 记录一次模型调用（不含 prompt/回复正文，也不含成本——
// 0027 起不存估算值，花费只看官方余额）。
func (r *CallLogRepo) InsertLLM(ctx context.Context, eventID, slot string, promptTokens, completionTokens int, finishReason string) error {
	query := r.store.Dialect().Rebind(`INSERT INTO llm_call_log
		(event_id, slot, prompt_tokens, completion_tokens, finish_reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if _, err := r.store.Writer().ExecContext(ctx, query, eventID, slot, promptTokens, completionTokens, finishReason, stamp(r.store, r.clk.Now())); err != nil {
		return fmt.Errorf("repo: insert llm log: %w", err)
	}
	return nil
}

// InsertTool 记录一次工具调用。
func (r *CallLogRepo) InsertTool(ctx context.Context, eventID, name string, isError bool, chars int) error {
	query := r.store.Dialect().Rebind(`INSERT INTO tool_call_log
		(event_id, name, is_error, chars, created_at) VALUES (?, ?, ?, ?, ?)`)
	if _, err := r.store.Writer().ExecContext(ctx, query, eventID, name, isError, chars, stamp(r.store, r.clk.Now())); err != nil {
		return fmt.Errorf("repo: insert tool log: %w", err)
	}
	return nil
}

var _ = time.Time{}
