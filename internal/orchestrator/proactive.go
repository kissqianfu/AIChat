package orchestrator

import (
	"context"
	"errors"

	"github.com/kissqianfu/aichat/internal/observability"
	"github.com/kissqianfu/aichat/internal/port"
)

// applyBudgetOrProactive 先走被动额度；用尽且档位与主动配额都允许时，
// 去掉锚点转主动发送（被动回复必须挂在收到的消息上，主动消息是独立推送）。
func (o *Orchestrator) applyBudgetOrProactive(ctx context.Context, ev port.Event, outs []port.Outbound) ([]port.Outbound, error) {
	budgeted, err := o.applyBudget(ctx, ev, outs)
	if err == nil {
		return budgeted, nil
	}
	if !errors.Is(err, ErrBudgetExhausted) || o.cfg.Proactive == nil {
		return nil, err
	}
	// 档位闸：只有 full 允许主动（policy.go 的 tierCaps），work/night/maint
	// 一律禁止——上班与睡眠时段宁可少发一条，也不把回复升级成推送打扰。
	//
	// 这里现算而不复用本轮的 policy 快照：判的就是"此刻允不允许推送"，
	// 到点即收紧（23:00 生成的回复不会因为开工时是 full 就放行），
	// 比沿用旧档位更贴语义；跨档瞬间的那一条也正该按新档处理。
	if policy := o.resolvePolicy(ctx, ev); !policy.Caps.Proactive {
		observability.CountDropped(observability.DropBudget)
		o.cfg.Logger.Info("档位禁止主动消息，放弃转主动发送",
			"event_id", ev.ID, "tier", string(policy.Tier), "segments", len(outs))
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
