package text

import (
	"regexp"
	"strings"
	"unicode"
)

// 入站正文里"看的不是字"的两类占位标记。
//
// placeholderFace 替换表情 markup，placeholderNoText 用于"被 @ 了但正文为空"。
// 二者都表示"这条消息没有可回应的文字"，回复缓存与补一句都要据此让路。
const (
	PlaceholderFace   = "〔表情〕"
	PlaceholderNoText = "@了我，但没有说话"
)

// faceMarkup 匹配 QQ 正文里的表情标签。
//
// 平台对所有表情包都发 faceType=6,faceId="0"、ext 里只有 {"text":""}、没有图片
// 地址——不同表情在正文里是一模一样的字节（2026-11-01 线上实测，四种表情落库
// 完全一致）。所以这里只能统一换成占位，分辨不出是哪个表情。
var faceMarkup = regexp.MustCompile(`<faceType=\d+,faceId="\d+",ext="[^"]*"\s*/?>`)

// NormalizeInbound 把入站正文里的平台 markup 换成可读占位并去掉首尾空白。
//
// 不做这层清洗会有两个后果（都已在线上出现）：模型会去"解读"这串字节并编出
// 画面（对着它描述"眼睛半睁着，还挂着口水"，其实根本没收到图）；不同表情的
// 正文完全相同，回复缓存的 key 也相同，于是逐字复读上一句。
func NormalizeInbound(s string) string {
	if s == "" {
		return ""
	}
	s = faceMarkup.ReplaceAllString(s, PlaceholderFace)
	// markup 前后常带空格，换成占位后中文里会多出空格（"〔表情〕 笑死"），一并去掉。
	s = strings.ReplaceAll(s, " "+PlaceholderFace, PlaceholderFace)
	s = strings.ReplaceAll(s, PlaceholderFace+" ", PlaceholderFace)
	return strings.TrimSpace(s)
}

// HasSubstance 判断正文里有没有实义内容（汉字/字母/数字）。
//
// 纯表情、纯 "@ 了没说话" 这类轮次没有"问题"可缓存，复刻上一句只会变成复读机；
// 也不值得再补一句。缓存与补一句都要求它返回 true。
func HasSubstance(s string) bool {
	s = strings.ReplaceAll(s, PlaceholderFace, "")
	s = strings.ReplaceAll(s, PlaceholderNoText, "")
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
