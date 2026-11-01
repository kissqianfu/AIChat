package prompt

import (
	"time"

	"github.com/kissqianfu/aichat/internal/platform/text"
	"github.com/kissqianfu/aichat/internal/port"
)

const (
	// TopicGap 是话题窗口：会话内相邻两条消息间隔超过它，即视为上一话题已结束。
	// 口径是“整个会话 3 分钟没消息”（群=整群没人说话，私聊=会话本身没消息），
	// 不是单个用户 3 分钟没说话。
	TopicGap = 3 * time.Minute
	// TopicScanLimit 是计算话题边界时扫的会话消息数：够覆盖“最近一次 ≥TopicGap
	// 的间隔”即可，多扫无益。
	TopicScanLimit = 50
	// topicGroupMax 是群公共层在当前话题内注入的上限——活跃群 3 分钟能刷几十条，
	// 不封顶会撑爆输入。
	topicGroupMax = 12
	// oldTopicMax 是引用恢复时旧话题块注入的上限。
	oldTopicMax = 12
	// quoteScanLimit 是引用锚点的内容匹配搜索范围（在最近这么多条里找被引用的原消息）。
	quoteScanLimit = 200
	// quoteAnchorMinRunes 是内容匹配的最短长度（rune）：更短的引用文本歧义太大，
	// 宁可不恢复也不错误恢复。
	quoteAnchorMinRunes = 6
)

// SegmentBounds 返回 msgs（时间正序）中包含下标 anchor 的话题段的 [start, end)
// 半开区间：从 anchor 往两侧扩，相邻消息间隔 ≥TopicGap 处切断。
// 时间戳为零值时全部视为同段（测试与脏数据的兜底，不会误切）。
func SegmentBounds(msgs []port.HistoryMessage, anchor int) (start, end int) {
	if len(msgs) == 0 {
		return 0, 0
	}
	if anchor < 0 {
		anchor = 0
	}
	if anchor >= len(msgs) {
		anchor = len(msgs) - 1
	}
	start = anchor
	for start > 0 && msgs[start].CreatedAt.Sub(msgs[start-1].CreatedAt) < TopicGap {
		start--
	}
	end = anchor + 1
	for end < len(msgs) && msgs[end].CreatedAt.Sub(msgs[end-1].CreatedAt) < TopicGap {
		end++
	}
	return start, end
}

// findQuoteAnchor 在 msgs（时间正序）里按"作者名一致（引用给出时）+ 文本互为前缀"
// 定位被引用消息的下标——引用文本可能被客户端截断，库里正文也可能被截断，取较短
// 一方作前缀比对。官方引用索引（REFIDX_）与库内 msg_id（ROBOT1.0_）不同值域，
// 无法按号定位，只能按内容找。找不到返回 -1。
func findQuoteAnchor(msgs []port.HistoryMessage, author, quoted string) int {
	qr := []rune(text.NormalizeInbound(quoted))
	if len(qr) < quoteAnchorMinRunes {
		return -1
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if author != "" && m.SenderName != author {
			continue
		}
		if !prefixEither([]rune(text.NormalizeInbound(m.Content)), qr) {
			continue
		}
		return i
	}
	return -1
}

// prefixEither 判断两串是否互为前缀（较短一方是较长一方的前缀）。
func prefixEither(a, b []rune) bool {
	short, long := a, b
	if len(short) > len(long) {
		short, long = b, a
	}
	if len(short) < quoteAnchorMinRunes {
		return false
	}
	return string(long[:len(short)]) == string(short)
}
