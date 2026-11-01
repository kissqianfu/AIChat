// commands.go：指令系统装配——admin 接线、注册表、/help 面板、/key /reload /confirm、认证码密钥。
package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/kissqianfu/QQ-SDK/event"
	"github.com/kissqianfu/aichat/internal/admin"
	"github.com/kissqianfu/aichat/internal/orchestrator"
	"github.com/kissqianfu/aichat/internal/platform/ids"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
)

// wireAdmin 构造管理指令层：注册全部指令、构造绘图指令与事件 handler。
func (w *wiring) wireAdmin(ctx context.Context, orch *orchestrator.Orchestrator) func(context.Context, *event.Event) {
	keyCmd := admin.NewKeyCommand(w.keyRepo, w.secretRepo, w.logger).WithAuditor(w.adminRepo).WithRoles(w.adminRepo)
	memCmd := admin.NewMemoryCommand(w.memoryRepo, w.secretRepo, w.logger).WithAuditor(w.adminRepo).WithRoles(w.adminRepo)
	authCmd := admin.NewAuthCommand(w.adminRepo, w.adminRepo, w.codeSecret(ctx), w.clk, w.logger).
		WithAuditor(w.adminRepo).WithOwnerNotifier(ownerNotifier(w.sender, w.logger))
	w.eventsRepo = repo.NewEventRepo(w.store, w.clk)
	hoursCmd := admin.NewHoursCommand(w.scheduler, w.secretRepo, w.logger).WithRoles(w.adminRepo).WithExceptions(w.schedStore)

	disp := admin.NewDispatcher(w.secretRepo, w.adminRepo, w.clk, w.logger)
	w.registerCommands(disp)

	w.followuper = w.buildFollowup()
	return newEventHandler(w.sender, orch, w.budget, keyCmd, memCmd, authCmd, hoursCmd,
		disp, w.groupRepo, w.banRepo, w.eventsRepo, w.self.ID, w.logger, w.followuper, w.statsObs,
		w.welcomeText)
}

// welcomeText 用 chat.fast 生成一句欢迎语（2026-10-31：欢迎语交由模型，无固定模板）。
// kind: "join"=机器人进群自我介绍；"member"=新成员入群。失败/超时/不安全一律退回固定短语，不阻塞入群流程。

// registerCommands 注册全部管理指令（docs/01 §6.5）；各 Run 体见下方具名函数。
// 2026-10-31 精简：/quota /model /admin /gadmin /ban /unban /interject /draw 移除
// （配额固定、模型槽位固定、拉黑/管理员由运维直改 DB、插话与画图由模型自行判断——画图走 image_gen 工具）。
func (w *wiring) registerCommands(disp *admin.Dispatcher) {
	confirmStore := admin.NewConfirmStore(w.clk)
	groupAdminRepo := repo.NewGroupAdminRepo(w.store, w.clk)
	disp.WithGroupAdmins(groupAdminRepo)

	disp.Register(admin.Command{Name: "help", Help: "查看指令", MinRole: admin.RoleUser, Run: w.helpRun(disp)})
	disp.Register(admin.Command{Name: "key", Help: "key list（owner）", MinRole: admin.RoleOwner, Run: w.keyRun()})
	disp.Register(admin.Command{Name: "reload", Help: "重载人设与模型配置（owner，需 /confirm）", MinRole: admin.RoleOwner, Run: w.reloadRun(confirmStore)})
	disp.Register(admin.Command{Name: "confirm", Help: "高危操作确认", MinRole: admin.RoleAdmin, Run: confirmRun(confirmStore)})
}

// helpRun /help：非管理员只见三行固定文案（docs/01 §6.5.2），管理员见指令清单。
func (w *wiring) helpRun(disp *admin.Dispatcher) func(context.Context, port.Event, []string) (string, bool) {
	return func(ctx context.Context, ev port.Event, _ []string) (string, bool) {
		if disp.RoleOf(ctx, ev) == admin.RoleUser {
			tier := "full"
			if policy, err := w.scheduler.Resolve(ctx, ""); err == nil {
				tier = string(policy.Tier)
			}
			return "我能做的：直接跟我聊，@我 或私聊都行。\n" +
				"想当管理员：找现有管理员要一个 24 位码，在私聊里直接发（要带开头的 /）。\n" +
				"当前时段：" + tier, true
		}
		role := disp.RoleOf(ctx, ev)
		w.sendHelpPanel(ctx, ev, role)
		return strings.Join(disp.Names(role), "\n"), true
	}
}

// sendHelpPanel 发送 /help 可点面板（docs/01 §6.5）：单聊指令按钮，点击自动发送对应指令。
// 仅管理员可见；发送失败（平台无键盘权限等）只告警——文本版 /help 始终先发，面板是增强。
func (w *wiring) sendHelpPanel(ctx context.Context, ev port.Event, role admin.Role) {
	if ev.Scene != port.SceneC2C || ev.MsgID == "" || w.budget == nil {
		return
	}
	// 按钮行（label ≤10 字，≤5 行×5 列，docs/01 §6.5.6 协议约束）。
	// 2026-10-31 二轮精简：/status、/remind 已删；三轮：/g、/gconfig 已删。
	rows := [][]port.Button{
		{{Label: "时段档位", Action: "/hours"}, {Label: "记忆", Action: "/memory"}, {Label: "帮助", Action: "/help"}},
	}
	if role >= admin.RoleAdmin {
		rows = append(rows,
			[]port.Button{{Label: "密钥注入", Action: "/key"}, {Label: "重载配置", Action: "/reload"}, {Label: "确认", Action: "/confirm"}},
		)
	}
	panel := port.Outbound{
		// 内嵌键盘官方要求 markdown 形态（docs/01 §8.2：Keyboard 仅 markdown 支持）。
		Intent: port.IntentMarkdown,
		Content: port.RichMessage{
			Blocks: []port.Block{
				port.TextBlock{Text: "点按钮直达（点一下自动发送指令）："},
				port.KeyboardBlock{Rows: rows},
			},
			Plain:    "点按钮直达（点一下自动发送指令）：",
			Markdown: "点按钮直达（点一下自动发送指令）：",
		},
	}
	seq, ok := w.budget.Take(ev.Scene, ev.MsgID)
	if !ok {
		w.logger.Warn("help 面板被动额度不足", "event_id", ev.ID)
		return
	}
	panel.Anchor = port.Anchor{MsgID: ev.MsgID}
	panel.Seq = seq
	if _, err := w.sender.Send(ctx, port.Target{Scene: ev.Scene, ID: ev.TargetID}, panel); err != nil {
		w.logger.Warn("help 面板发送失败", "event_id", ev.ID, "err", err)
	}
}

// keyRun /key list：密钥清单（owner）。
func (w *wiring) keyRun() func(context.Context, port.Event, []string) (string, bool) {
	return func(ctx context.Context, _ port.Event, _ []string) (string, bool) {
		metas, err := w.keyRepo.ListMeta(ctx)
		if err != nil {
			return "读取 key 失败", true
		}
		var b strings.Builder
		for _, m := range metas {
			fmt.Fprintf(&b, "%s tail=%s v%d\n", m.Name, m.Tail, m.Version)
		}
		return strings.TrimSpace(b.String()), true
	}
}

// reloadRun /reload：人设与模型配置重载（owner，需 /confirm 二次确认）。
func (w *wiring) reloadRun(confirmStore *admin.ConfirmStore) func(context.Context, port.Event, []string) (string, bool) {
	return func(ctx context.Context, ev port.Event, _ []string) (string, bool) {
		if ev.Scene != port.SceneC2C {
			return "", true // 指令只走私聊；群内静默
		}
		token, err := ids.Short(8)
		if err != nil {
			return "生成确认码失败", true
		}
		confirmStore.IssueAction(token, func(runCtx context.Context) (string, error) {
			personaReply, err := reloadPersona(runCtx, w.personaStore, w.adminRepo, ev.Author.ID, w.logger)
			if err != nil {
				return personaReply, err
			}
			modelsReply := reloadModels(runCtx, w.reg, w.adminRepo, ev.Author.ID, w.logger)
			return personaReply + "；" + modelsReply, nil
		})
		return "确认 reload：5 分钟内回复 /confirm " + token, true
	}
}

// confirmRun /confirm：高危操作确认（确认码一次性）。
func confirmRun(confirmStore *admin.ConfirmStore) func(context.Context, port.Event, []string) (string, bool) {
	return func(ctx context.Context, ev port.Event, args []string) (string, bool) {
		if ev.Scene != port.SceneC2C {
			return "", true // 指令只走私聊；群内静默
		}
		if len(args) < 1 {
			return "用法：/confirm <确认码>", true
		}
		action, ok := confirmStore.Consume(args[0])
		if !ok {
			return "确认码无效或已过期", true
		}
		if action == nil {
			return "已确认", true
		}
		reply, err := action(ctx)
		if err != nil {
			return "执行失败：" + err.Error(), true
		}
		return reply, true
	}
}

// remindRun 已随 2026-10-31 指令精简移除（/remind list|cancel）：
// 提醒仍可由模型经 reminder 工具设置、到点自动发送，只是不再暴露查询/取消指令。

// startReminderLoop 提醒到点发送（M7-4b）：走主动消息配额；发送成功才标记，失败下轮重试（超 24h 丢弃）。

// codeSecret 返回认证码 HMAC 密钥：首次启动随机生成 32 字节并存 bot_secret 表，
// 之后从库里读（用户拍板：密钥不进配置文件、不走 env）。
func (w *wiring) codeSecret(ctx context.Context) string {
	secret, err := w.secretRepo.Secret(ctx, repo.AdminCodeKeyName)
	if err != nil {
		w.logger.Warn("读取认证码密钥失败", "err", err)
	}
	if secret != "" {
		return secret
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		w.logger.Error("生成认证码密钥失败，认证码不可用", "err", err)
		return ""
	}
	secret = hex.EncodeToString(raw)
	if err := w.secretRepo.Set(ctx, repo.AdminCodeKeyName, secret, "认证码 HMAC 密钥（启动时自动生成）"); err != nil {
		w.logger.Error("认证码密钥写入 bot_secret 失败，认证码不可用", "err", err)
		return ""
	}
	w.logger.Info("认证码 HMAC 密钥已生成并写入 bot_secret 表")
	return secret
}

// keyGetter 是 api_key 表的取密钥能力（repo.APIKeyRepo 实现）。
