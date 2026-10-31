package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrConfigMissing 表示配置文件不存在（首次运行）。
var ErrConfigMissing = errors.New("config: 配置文件不存在")

// Template 是首次运行生成的模板配置（最小可启动集 + 常用项注释）。
// 生成后直接重启即可跑通（密钥仍需按注释注入）。
const Template = `# aichat 运行配置（不含任何密钥；所有字段都有默认值，这里只写要改的项）
#
# 密钥位置（首次启动前注入，见 README）：
#     CLI: aichat-db -config config.yaml key set deepseek

# 事件接收方式
bot:
  transport: webhook          # websocket | webhook | both

webhook:
  addr: "127.0.0.1:23015"     # 只监听回环；公网由 Nginx 反代
  path: /aichat/webhook

app:
  log_level: info             # debug | info | warn | error（日志恒为 JSON）
  # log_file: ./data/aichat.log   # 空 = 只进 stderr；配置 = 文件+stderr 双写（append）
  # pid_file: /run/aichat/aichat.pid  # 空 = 不写；配置 = 启动写入、退出删除
  timezone: Asia/Shanghai     # 必填，缺失拒绝启动

# 数据库
db:
  driver: sqlite              # sqlite | postgres
  sqlite:
    path: ./data/aichat.db
    busy_timeout: 5s
  backup_dir: ./data/backup
  auto_migrate: true          # 首次启动自动建库建表

# 人设
persona:
  dir: ./persona
  file_text: persona.md
  file_meta: persona.yaml
  watch: true

# 模型槽位：每个引用自带 base_url 与 api_key_name（→ api_key 表）。
models:
  # 聊天
  chat:
    # 日常闲聊，默认槽位
    fast:
      primary: { base_url: https://api.deepseek.com, model: deepseek-flash, api_key_name: deepseek }
    # 复杂推理/长文
    strong: 
      primary: { base_url: https://api.deepseek.com, model: deepseek-v4-pro, api_key_name: deepseek }
  # 图像理解：不用时整段删除 vision 段（写了就必须给 base_url/model，不支持留空关闭）
  vision:
    primary: { base_url: https://api.deepseek.com, model: deepseek-flash, api_key_name: deepseek }
  # 语义缓存：base_url 留空 = 关闭
  embed:
    primary: { base_url: "", model: text-embedding-3-small, api_key_name: openai }
  # 语音识别：base_url 留空 = 关闭
  asr:
    primary: { base_url: https://maas.qianwenaiapi.com/compatible-mode/v1, model: qwen3-asr-flash, api_key_name: asr, protocol: qianwen }
  # 语音合成：base_url 留空 = 关闭
  tts:
    primary: { base_url: https://api.breeze.blue/v1, model: breeze-tts-2, voice: voc_p36dbdewwvae, api_key_name: breezeblue, protocol: breeze }
  # 图像生成：base_url 留空 = 关闭；且需 imagegen.enabled = true 再群内 /draw on
  imagegen:
    primary: { base_url: "", model: dall-e-3, size: 1024x1024, api_key_name: openai }
  # 联网搜索/抓取
  search:
    primary: { base_url: https://api.deepseek.com, api_key_name: deepseek, protocol: deepseek }


# 功能灰度开关（默认全 true）
features:
  cache: true           # 回复缓存
  semantic_cache: true  # 语义缓存
  memory_extract: true  # 记忆抽取
  tools: true           # 工具调用
  followup: true        # 追问引导
  interject: true       # 插话自决
`

// WriteTemplate 把模板配置写到 path（父目录自动创建）。已存在时不覆盖，返回 false。
func WriteTemplate(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, fmt.Errorf("config: 建目录失败: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(Template), 0o600); err != nil {
		return false, fmt.Errorf("config: 写模板失败: %w", err)
	}
	return true, nil
}
