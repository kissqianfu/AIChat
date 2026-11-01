package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/clock"
)

// GlobalKey 是全局 override 的固定 key；其余 key 直接使用群 openid。
const GlobalKey = "global"

// locShanghai 是策略判定使用的固定时区。
//
// 时区写死而不是读配置：分时段规则的语义（09:00 上班）只在北京时间下成立。
// 极简容器可能没有 tzdata，LoadLocation 失败时退回固定 UTC+8（北京时间无夏令时，等价）。
var locShanghai = loadShanghai()

func loadShanghai() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.FixedZone("UTC+8", 8*60*60)
}

// errNoStore 表示调用方忘了注入 OverrideStore。
//
// 不允许静默跳过：跳过 override 会让"临时维护档"失效，表现为重启后机器人立刻恢复全功能。
var errNoStore = errors.New("schedule: 未注入 OverrideStore")

// Scheduler 是分时段策略判定器。
//
// 它只持有时间源与 override 存储，不缓存、不起定时任务：时段是纯函数 f(now)，
// 进程重启不会漏掉档位切换（docs/09 §5.3）。
type Scheduler struct {
	clk   clock.Clock
	store OverrideStore
	exc   ExceptionStore
	loc   *time.Location

	mu   sync.RWMutex
	wins []Window
}

// New 构造 Scheduler；clk 为 nil 时退回系统时钟，store 为 nil 时 Resolve/SetOverride 返回错误。
func New(clk clock.Clock, store OverrideStore) *Scheduler {
	if clk == nil {
		clk = clock.System()
	}
	return &Scheduler{clk: clk, store: store, loc: locShanghai, wins: DefaultWindows()}
}

// WithWindows 覆盖默认时段规则（config 的 hours.windows 列表；不调则用内置默认）。
func (s *Scheduler) WithWindows(wins []Window) *Scheduler {
	s.SetWindows(wins)
	return s
}

// SetWindows 热更新时段规则（config hours 段热重载用，docs/08 §2.6）。
// 空列表视为「清空自定义、回到内置默认」——与启动时 windows 为空的语义一致。
func (s *Scheduler) SetWindows(wins []Window) {
	if len(wins) == 0 {
		wins = DefaultWindows()
	}
	s.mu.Lock()
	s.wins = wins
	s.mu.Unlock()
}

// Resolve 判定 groupID 当前应使用的策略。
//
// 优先级：override > 例外日 > 默认规则（work/quiet/free），docs/09 §4.4。
// override 与例外日命中即返回、不参与取严；groupID 为空表示只看全局（私聊/无群上下文）。
// 判定基于注入的 clock，一轮对话内应由调用方持有同一份 Policy（Sticky）。
func (s *Scheduler) Resolve(ctx context.Context, groupID string) (Policy, error) {
	if s.store == nil {
		return Policy{}, errNoStore
	}
	now := s.clk.Now()

	// 群级 override 优先；"global" 是全局 key，不当作群 id 重复查询。
	groupID = strings.TrimSpace(groupID)
	if groupID != "" && groupID != GlobalKey {
		ov, ok, err := s.store.Get(ctx, groupID)
		if err != nil {
			return Policy{}, fmt.Errorf("schedule: 读取群 override %q: %w", groupID, err)
		}
		if ok && ov.ValidAt(now) {
			return policyFor(ov.Tier)
		}
	}

	ov, ok, err := s.store.Get(ctx, GlobalKey)
	if err != nil {
		return Policy{}, fmt.Errorf("schedule: 读取全局 override: %w", err)
	}
	if ok && ov.ValidAt(now) {
		return policyFor(ov.Tier)
	}

	// 例外日（节假日 / 调休）：群级优先于全局，命中即返回。
	if policy, hit, err := s.resolveException(ctx, groupID, s.exceptionDate(now)); err != nil {
		return Policy{}, err
	} else if hit {
		return policy, nil
	}

	// 无有效 override 与例外日，按固定时区套默认规则。
	s.mu.RLock()
	tier := matchDefaultTier(now.In(s.loc), s.wins)
	s.mu.RUnlock()
	return policyFor(tier)
}

// SetOverride 写入一条临时强制档位，ttl 后自动失效（到点即失效，半开区间）。
//
// key 用 "global" 或群 openid；同 key 重复设置即覆盖。
func (s *Scheduler) SetOverride(ctx context.Context, key, tier string, ttl time.Duration) error {
	if s.store == nil {
		return errNoStore
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("schedule: override key 不能为空")
	}
	t, err := ParseTier(tier)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		return fmt.Errorf("schedule: override ttl 必须为正数，当前 %v", ttl)
	}
	now := s.clk.Now()
	return s.store.Put(ctx, Override{Key: key, Tier: t, ExpiresAt: now.Add(ttl), CreatedAt: now})
}

// ClearOverride 删除一条临时强制档位；key 不存在时不报错（幂等）。
func (s *Scheduler) ClearOverride(ctx context.Context, key string) error {
	if s.store == nil {
		return errNoStore
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("schedule: override key 不能为空")
	}
	return s.store.Delete(ctx, key)
}
