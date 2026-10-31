package redact

import "regexp"

// sensitivePatterns 是"不该写进记忆/日志"的敏感信息模式（M2-6 隐私闸门）。
var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`1[3-9]\d{9}`),              // 手机号
	regexp.MustCompile(`\b\d{17}[\dXx]\b`),         // 身份证（18 位）
	regexp.MustCompile(`\b\d{16,19}\b`),            // 银行卡
	regexp.MustCompile(`(?i)\bsk-[a-z0-9]{16,}\b`), // API key
	regexp.MustCompile(`(?i)(密码|验证码|身份证|银行卡|住址|家庭住址|手机号|QQ号)`),
}

// Sensitive 判断文本是否包含敏感信息（命中任一模式即 true）。
//
// 记忆入库、显式"记住"指令都先用它拦一道；日志脱敏仍走 Field/Key。
func Sensitive(s string) bool {
	if s == "" {
		return false
	}
	for _, re := range sensitivePatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
