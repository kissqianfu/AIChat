package emotion

import (
	"github.com/kissqianfu/aichat/internal/port"
)

// perTurnMaxDelta 是单轮情绪增量上限（安全阀，docs/01 §5）。
const perTurnMaxDelta = 0.15

// Style is the reply style derived from emotion (M7-7).
type Style struct {
	MaxRunes   int // 0 = unlimited
	AllowEmoji bool
	Exclaim    bool
	Initiative float64 // 0..1
}

// StyleFor maps emotions to style parameters.
func StyleFor(e port.Emotions) Style {
	style := Style{MaxRunes: 400, AllowEmoji: true, Exclaim: true, Initiative: 0.5}
	switch {
	case e.Anger > 0.4:
		style.MaxRunes = 120
		style.AllowEmoji = false
		style.Exclaim = false
		style.Initiative = 0.2
	case e.Sorrow > 0.4:
		style.MaxRunes = 200
		style.AllowEmoji = false
		style.Exclaim = false
		style.Initiative = 0.3
	case e.Joy > 0.6:
		style.MaxRunes = 500
		style.Initiative = 0.8
	case e.Love > 0.6:
		style.Initiative = 0.7
	}
	return style
}

// appraisalRule 是评估表的一行：命中关键词 → 该轮情绪增量。
type appraisalRule struct {
	name   string
	words  []string
	deltas port.Emotions
}

// appraisalTable 是"七情"评估表（M7-7）：顺序即优先级，首条命中生效。
var appraisalTable = []appraisalRule{
	{name: "affection", words: []string{"喜欢你", "爱你", "谢谢", "厉害", "可爱", "抱抱"},
		deltas: port.Emotions{Love: 0.12, Joy: 0.10}},
	{name: "anger", words: []string{"讨厌", "滚", "傻", "烦", "闭嘴", "垃圾"},
		deltas: port.Emotions{Anger: 0.14, Joy: -0.08}},
	{name: "sorrow", words: []string{"难过", "伤心", "哭", "累了", "压力"},
		deltas: port.Emotions{Sorrow: 0.12, Love: 0.04}},
	{name: "curiosity", words: []string{"？", "?"},
		deltas: port.Emotions{Joy: 0.02}},
}

// Appraise 按评估表返回本轮的七情增量（每条 |Δ| ≤ perTurnMaxDelta）。
func Appraise(text string) port.Emotions {
	for _, rule := range appraisalTable {
		if containsAny(text, rule.words...) {
			return clampDeltas(rule.deltas)
		}
	}
	return port.Emotions{}
}

// clampDeltas 把单轮增量限制在 ±perTurnMaxDelta。
func clampDeltas(d port.Emotions) port.Emotions {
	clampDelta := func(v float64) float64 {
		if v > perTurnMaxDelta {
			return perTurnMaxDelta
		}
		if v < -perTurnMaxDelta {
			return -perTurnMaxDelta
		}
		return v
	}
	return port.Emotions{
		Joy: clampDelta(d.Joy), Anger: clampDelta(d.Anger), Sorrow: clampDelta(d.Sorrow),
		Fear: clampDelta(d.Fear), Love: clampDelta(d.Love), Disgust: clampDelta(d.Disgust),
		Desire: clampDelta(d.Desire),
	}
}
