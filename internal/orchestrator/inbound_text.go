package orchestrator

import (
	"regexp"
	"strings"
	"unicode"
)

// placeholderFace 是表情标签的替代文本。
//
// QQ 正文里的表情是 markup（`<faceType=6,faceId="0",ext="...">`），而且**不同的
// 表情包在正文里完全一样**：线上实测四种不同表情，落库是同一串字节（faceId 恒为 0、
// ext 里没有文字），连图片地址都没有。把 markup 原样喂给模型有两层坏处：
//
//  1. 模型会去"解读"这串字节，编出画面。线上实测它对着它描述"眼睛半睁着，还挂着
//     口水"——我们根本没收到图，这是纯粹的幻觉。
//  2. 归一化前不同表情的文本相同，回复缓存的 key 也相同，10 分钟内第二张起直接
//     复读上一句（见 cache.go 的 cacheable）。
//
// 所以统一换成〔表情〕：模型知道"有人发了个表情"，但手上没有任何可编的细节。
const placeholderFace = "〔表情〕"

// placeholderNoText 是"被 @ 了但正文为空"的占位输入。
const placeholderNoText = "@了我，但没有说话"

// faceMarkup 匹配 QQ 正文里的表情标签。
var faceMarkup = regexp.MustCompile(`<faceType=\d+,faceId="\d+",ext="[^"]*"\s*/?>`)

// normalizeInbound 清掉正文里的平台 markup，返回可读文本。
func normalizeInbound(s string) string {
	if s == "" {
		return ""
	}
	return strings.TrimSpace(faceMarkup.ReplaceAllString(s, placeholderFace))
}

// substantiveText 判断输入里有没有"实义内容"（汉字/字母/数字）。
//
// 纯表情、纯 "@我但没说话" 这类轮次没有可缓存的"问题"：回复该由当轮上下文决定，
// 复刻上一句只会变成复读机。缓存读写都要求它返回 true。
func substantiveText(s string) bool {
	s = strings.ReplaceAll(s, placeholderFace, "")
	s = strings.ReplaceAll(s, placeholderNoText, "")
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
