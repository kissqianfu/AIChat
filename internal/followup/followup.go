// Package followup 是"补一句"行为（P1 拟人化，docs/01 §2.6）：
// 回复发出后延迟 45-150 秒，用 chat.fast 判断"要不要再自然地补一句"。
//
// 四重刹车（任一命中即放弃，按成本从低到高排列）：
//  1. 30% 概率（掷骰子，大部分回复本来就不补）；
//  2. 对方没再发言（对方又说了话就交给正常回复链路）；
//  3. 群不吵（2 分钟内入站消息 > 3 条说明话题正在滚动，补一句会打断）；
//  4. 被动额度够（复用同一 msg_id 的被动额度，群窗口 5min > 延迟上限 150s，必然够窗口）。
//
// 判断/生成都走 chat.fast 小模型；补出的句子仍走被动通道（带 msg_id 锚点），
// 不消耗主动消息额度——主动额度是 P2 主动开口专属。
package followup

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/orchestrator"
	"github.com/kissqianfu/aichat/internal/platform/clock"
	"github.com/kissqianfu/aichat/internal/port"
	"github.com/kissqianfu/aichat/internal/safety"
)

// 默认参数（写死不进配置：行为层参数与代码常量只有一个说了算）。
const (
	defaultProbability = 0.3 // 概率刹车
	defaultMinDelay    = 45 * time.Second
	defaultMaxDelay    = 150 * time.Second
	quietWindow        = 2 * time.Minute // "群不吵"观察窗
	noisyCount         = 3               // 窗口内入站消息 > 3 条 = 吵
	recentCap          = 64              // 每会话保留的最近入站时间数
	judgeTimeout       = 5 * time.Second
	genTimeout         = 10 * time.Second
	genMaxTokens       = 96
	followupMaxRunes   = 60 // 补一句的上限：一句话的长度

	judgeSystemPrompt = "你在判断要不要再补一句话。刚才是机器人（你）的回复：如果还有值得自然补充的小信息、小吐槽或一句收尾，回答 yes；如果已经说完整了，回答 no。只回答 yes 或 no。"
	genSystemPrompt   = "基于刚才的对话，用一句话自然地补一句（不超过40个字）：补充一个小细节、一句吐槽或一个自然的收尾。只输出这一句话，不要解释，不要重复刚才说过的内容。"
)

// Options 是可注入的参数（零值 = 默认）；Dice 供测试控制概率刹车。
type Options struct {
	Probability float64
	MinDelay    time.Duration
	MaxDelay    time.Duration
	Dice        func() float64
}

// ModelSource 是 chat.fast 槽位的取模型能力。
type ModelSource func(ctx context.Context) (port.ChatModel, error)

// Followup 管理补一句的观察与排期；零值不可用，用 New 构造。
// 依赖不齐（sender/model 缺失）时 New 返回 nil，调用方直接判空跳过。
type Followup struct {
	sender   port.Sender
	budget   *orchestrator.Budget
	recorder port.Recorder
	model    ModelSource
	clk      clock.Clock
	log      *slog.Logger
	opts     Options

	mu  sync.Mutex
	act map[string]*activity
}

// activity 是一个会话的入站观察（群按群、单聊按用户）。
type activity struct {
	recent     []time.Time          // 最近入站消息时间（有界）
	lastByUser map[string]time.Time // 每用户最后一次发言
}

// New 构造补一句管理器。
func New(sender port.Sender, budget *orchestrator.Budget, recorder port.Recorder,
	model ModelSource, clk clock.Clock, logger *slog.Logger, opts Options) *Followup {
	if sender == nil || model == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if clk == nil {
		clk = clock.System()
	}
	if opts.Probability <= 0 || opts.Probability > 1 {
		opts.Probability = defaultProbability
	}
	if opts.MinDelay <= 0 {
		opts.MinDelay = defaultMinDelay
	}
	if opts.MaxDelay < opts.MinDelay {
		opts.MaxDelay = opts.MinDelay + (defaultMaxDelay - defaultMinDelay)
	}
	if opts.Dice == nil {
		opts.Dice = rand.Float64 // #nosec G404 -- 概率刹车，非安全用途
	}
	return &Followup{
		sender: sender, budget: budget, recorder: recorder, model: model,
		clk: clk, log: logger, opts: opts, act: make(map[string]*activity),
	}
}

// Observe 记录一条入站消息（每条消息事件都调，包括不回复的）。
func (f *Followup) Observe(ev port.Event) {
	if f == nil || ev.Author.ID == "" {
		return
	}
	now := f.clk.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.act[sessionKey(ev.Scene, ev.TargetID)]
	if a == nil {
		a = &activity{lastByUser: make(map[string]time.Time)}
		f.act[sessionKey(ev.Scene, ev.TargetID)] = a
	}
	a.recent = append(a.recent, now)
	if len(a.recent) > recentCap {
		a.recent = a.recent[len(a.recent)-recentCap:]
	}
	a.lastByUser[ev.Author.ID] = now
}

// Schedule 在回复发出后排期一次补一句（45-150s 随机延迟）。
// ev 是触发回复的那条入站消息；reply 是已发出的回复正文（取首段即可）。
func (f *Followup) Schedule(ev port.Event, reply string) {
	if f == nil || ev.MsgID == "" || strings.TrimSpace(reply) == "" {
		return
	}
	userText := strings.TrimSpace(firstText(ev.Parts))
	if userText == "" {
		return // 对图片/语音等非文本消息不做补一句
	}
	f.mu.Lock()
	a := f.act[sessionKey(ev.Scene, ev.TargetID)]
	var userLast time.Time
	if a != nil {
		userLast = a.lastByUser[ev.Author.ID]
	}
	f.mu.Unlock()
	delay := f.opts.MinDelay
	if span := f.opts.MaxDelay - f.opts.MinDelay; span > 0 {
		delay += time.Duration(rand.Int64N(int64(span))) // #nosec G404
	}
	f.log.Info("已排期补一句", "event_id", ev.ID, "delay", delay.Round(time.Second).String())
	time.AfterFunc(delay, func() { f.fire(ev, userText, reply, userLast) })
}

// fire 延迟到点执行：四重刹车 → 模型判断 → 生成 → 过额度 → 发送。
func (f *Followup) fire(ev port.Event, userText, reply string, userLast time.Time) {
	// 刹车 1：概率。
	if f.opts.Dice() > f.opts.Probability {
		f.drop(ev, "概率刹车")
		return
	}
	// 刹车 2/3：对方没再发言 + 群不吵。
	f.mu.Lock()
	a := f.act[sessionKey(ev.Scene, ev.TargetID)]
	spokeAgain, noisy := false, 0
	if a != nil {
		spokeAgain = a.lastByUser[ev.Author.ID].After(userLast)
		windowStart := f.clk.Now().Add(-quietWindow)
		for _, t := range a.recent {
			if t.After(windowStart) {
				noisy++
			}
		}
	}
	f.mu.Unlock()
	if spokeAgain {
		f.drop(ev, "对方又发言了")
		return
	}
	if noisy > noisyCount {
		f.drop(ev, "群太吵")
		return
	}
	// 刹车 4：被动额度（内存预检，最终以 Take 为准）。
	if f.budget != nil && f.budget.PassiveLeft(ev.Scene, ev.MsgID) <= 0 {
		f.drop(ev, "被动额度已用完")
		return
	}
	// 模型判断要不要补。
	judgeCtx, cancel := context.WithTimeout(context.Background(), judgeTimeout)
	answer, ok := f.ask(judgeCtx, judgeSystemPrompt, dialogueOf(userText, reply))
	cancel()
	if !ok || !strings.Contains(strings.ToLower(answer), "yes") {
		f.drop(ev, "模型判断不补")
		return
	}
	// 生成补的一句。
	genCtx, cancel := context.WithTimeout(context.Background(), genTimeout)
	defer cancel()
	text, ok := f.ask(genCtx, genSystemPrompt, dialogueOf(userText, reply))
	if !ok {
		f.drop(ev, "生成失败")
		return
	}
	text = truncateRunes(strings.TrimSpace(text), followupMaxRunes)
	if text == "" {
		f.drop(ev, "生成内容为空")
		return
	}
	if safe, _ := safety.Check(text); !safe {
		f.drop(ev, "安全检查未过")
		return
	}
	// 过被动额度（同一 msg_id 锚点，msg_seq 续接）并发送。
	out := port.Outbound{
		Intent:  port.IntentText,
		Content: port.RichMessage{Plain: text, Blocks: []port.Block{port.TextBlock{Text: text}}},
	}
	if f.budget != nil {
		seq, ok := f.budget.TakeCtx(genCtx, ev.Scene, ev.MsgID)
		if !ok {
			f.drop(ev, "被动额度已用完")
			return
		}
		out.Anchor = port.Anchor{MsgID: ev.MsgID}
		out.Seq = seq
	}
	if _, err := f.sender.Send(genCtx, port.Target{Scene: ev.Scene, ID: ev.TargetID}, out); err != nil {
		f.log.Warn("补一句发送失败", "event_id", ev.ID, "err", err)
		return
	}
	f.log.Info("补一句已发送", "event_id", ev.ID, "text", text)
	if f.recorder != nil {
		scene := "c2c"
		if ev.Scene == port.SceneGroup {
			scene = "group"
		}
		rec := port.MessageRecord{EventID: "followup-" + ev.ID, Direction: "out", Scene: scene, TargetID: ev.TargetID, Content: text}
		if err := f.recorder.SaveMessage(genCtx, rec); err != nil {
			f.log.Warn("补一句落库失败", "event_id", ev.ID, "err", err)
		}
	}
}

// drop 记录一次放弃（debug 级别，避免刷日志）。
func (f *Followup) drop(ev port.Event, reason string) {
	f.log.Debug("补一句放弃", "event_id", ev.ID, "reason", reason)
}

// ask 调一次 chat.fast；超时/报错/空回答返回 ok=false。
func (f *Followup) ask(ctx context.Context, system, user string) (string, bool) {
	model, err := f.model(ctx)
	if err != nil || model == nil {
		return "", false
	}
	resp, err := model.Chat(ctx, port.ChatRequest{
		Messages: []port.ChatMessage{
			{Role: port.ChatRoleSystem, Content: system},
			{Role: port.ChatRoleUser, Content: user},
		},
		MaxTokens: genMaxTokens,
	})
	if err != nil || resp == nil {
		return "", false
	}
	answer := strings.TrimSpace(resp.Content)
	return answer, answer != ""
}

// sessionKey 是会话观察键（群按群、单聊按用户）。
func sessionKey(scene port.Scene, target string) string {
	return strconv.Itoa(int(scene)) + "|" + target
}

// dialogueOf 拼判断/生成用的对话文本。
func dialogueOf(userText, reply string) string {
	return "对方说：" + userText + "\n机器人回了：" + reply
}

// firstText 取第一段文本（与 orchestrator.firstText 同义，避免反向依赖）。
func firstText(parts []port.Part) string {
	for _, p := range parts {
		if p.Kind == port.PartText {
			return p.Text
		}
	}
	return ""
}

// truncateRunes 按 rune 截断。
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
