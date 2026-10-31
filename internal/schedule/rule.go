package schedule

import "time"

// 默认时段（可被 config 的 hours.windows 列表覆盖，见 WithWindows）：
//   - 周一至周五 09:00-18:00 = work；
//   - 每天 23:00-07:00 = night（跨零点）；
//   - 其余时间 = full；
//   - maint 不在默认规则里，只能通过临时 override 进入。
const (
	defWorkStartMin  = 9 * 60  // 540 = 09:00
	defWorkEndMin    = 18 * 60 // 1080 = 18:00
	defNightStartMin = 23 * 60 // 1380 = 23:00
	defNightEndMin   = 7 * 60  // 420 = 07:00
)

// Window 是一条默认规则：days 命中的星期里，[startMin, endMin) 区间内进 tier 档。
//
// days 用 bitmask（bit0=周一 … bit6=周日，与 config.ParseDays 的输出一致）；
// 区间是半开的，startMin > endMin 表示跨零点。
type Window struct {
	StartMin int
	EndMin   int
	Days     uint8
	Tier     Tier
}

// DefaultWindows 返回内置默认规则（按顺序判定，首中即返回）。
func DefaultWindows() []Window {
	return []Window{
		{StartMin: defWorkStartMin, EndMin: defWorkEndMin, Days: 0x1F, Tier: TierWork},
		{StartMin: defNightStartMin, EndMin: defNightEndMin, Days: 0x7F, Tier: TierNight},
	}
}

// matchDefaultTier 按默认规则判定档位（顺序判定，首中即返回）；now 必须已转换到 schedule 时区。
func matchDefaultTier(now time.Time, wins []Window) Tier {
	for _, w := range wins {
		if w.daysHit(now) && inMinuteRange(minuteOfDay(now), w.StartMin, w.EndMin) {
			return w.Tier
		}
	}
	return TierFull
}

// daysHit 判断 now 的星期是否命中本窗口。
//
// time.Weekday 里周日是 0，先转成 ISO 8601（周一=1 … 周日=7），
// 与文档、数据库里的 days 表示以及 config.ParseDays 的 bitmask 保持一致。
func (w Window) daysHit(now time.Time) bool {
	iso := int(now.Weekday())
	if iso == 0 {
		iso = 7
	}
	return w.Days&(1<<(iso-1)) != 0
}

// minuteOfDay 返回当天从 00:00 起的分钟数。
func minuteOfDay(t time.Time) int {
	return t.Hour()*60 + t.Minute()
}

// inMinuteRange 判断 m 是否落在半开区间 [start, end)。
//
// 18:00:00 必须属于下一档而不是 work，所以右端必须开；
// start > end 表示跨零点；start == end 自然退化为全天（docs/09 §8.1 的约定）。
func inMinuteRange(m, start, end int) bool {
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end
}
