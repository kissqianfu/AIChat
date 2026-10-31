package orchestrator

import (
	"context"
	"errors"

	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/port"
)

// applyBudgetOrProactive 先走被动额度；用尽且主动配额允许时，去掉锚点转主动发送。
func (o *Orchestrator) applyBudgetOrProactive(ctx context.Context, ev port.Event, outs []port.Outbound) ([]port.Outbound, error) {
	budgeted, err := o.applyBudget(ctx, ev, outs)
	if err == nil {
		return budgeted, nil
	}
	if !errors.Is(err, ErrBudgetExhausted) || o.cfg.Proactive == nil {
		return nil, err
	}
	allowed, perr := o.cfg.Proactive(ctx, ev)
	if perr != nil || !allowed {
		observability.CountDropped(observability.DropBudget)
		return nil, err
	}
	for i := range outs {
		outs[i].Anchor = port.Anchor{}
		outs[i].Seq = i + 1
	}
	observability.CountProactiveSent(observability.ProactiveFallback)
	o.cfg.Logger.Info("被动额度用尽，转主动消息", "event_id", ev.ID, "segments", len(outs))
	return outs, nil
}
