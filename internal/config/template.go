package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrConfigMissing 表示配置文件不存在（首次运行）。
var ErrConfigMissing = errors.New("config: 配置文件不存在")

// Template 是首次运行生成的模板配置。
//
// 写法：列出全部可调项，值即内置默认值（interject 例外，见该行注释），
// 不需要的整段删掉即可——缺省项自动取默认，模板本身能直接启动（密钥另注入）。
const Template = `# aichat 运行配置（不含任何密钥）
#
# 下面列出全部可调项，值即内置默认值；不需要的整段删掉，缺省项自动取默认。
# 密钥不进本文件（首次启动前注入，见 README）：
#     aichat-db -config config.yaml key set deepseek        # 模型密钥（api_key 表）
#     aichat-db -config config.yaml secret set qq_app_secret # 平台密钥（bot_secret 表）

# 进程
app:
  log_level: info                  # debug | info | warn | error（日志恒为 JSON）
  log_file: ""                     # 空 = 只进 stderr；配置 = 文件+stderr 双写（append，不轮转）
  pid_file: ""                     # 空 = 不写；配置 = 启动写入、退出删除
  timezone: Asia/Shanghai          # 必填，缺失拒绝启动

# 事件接收方式
bot:
  transport: webhook               # websocket | webhook | both

webhook:
  addr: "127.0.0.1:23015"          # 只监听回环；公网由 Nginx 反代，TLS 在 Nginx 终止
  path: /aichat/webhook

# 存储
db:
  driver: sqlite                   # sqlite | postgres
  sqlite:
    path: ./data/aichat.db
    busy_timeout: 5s
  postgres:                        # 仅 driver: postgres 时生效
    dsn: ""                        # 建议写 ${PG_DSN} 由环境变量注入，别把明文连接串放进文件
    max_open_conns: 10
    max_idle_conns: 4
  backup_dir: ./data/backup
  auto_migrate: true               # 首次启动自动建库建表

# 分时段档位
hours:
  windows:
    - { days: "1-5", range: "09:00-12:00", tier: work }   # 工作日上班
    - { days: "1-5", range: "14:00-18:00", tier: work }
    - { days: "*",  range: "23:00-07:00", tier: night }   # 每天深夜


# 人设（唯一真源在 persona/ 目录，不进库）
persona:
  dir: ./persona
  file_text: persona.md
  file_meta: persona.yaml
  watch: true                      # 监听改动即时生效，无需重启
  max_bytes: 65536                 # 单个文件上限

# 记忆
memory:
  importance_archive: 0.55         # 重要度 ≥ 此值的记忆转长期存储
  r_archive: 0.20                  # 召回阈值（越低越容易想起旧事）
  consolidate_cron: "0 3 * * *"    # 每日整理时间（cron，按 app.timezone）
  episode_turns: 6                 # 每 N 轮生成一条情景记忆；0 = 关闭
  relationship_turns: 15           # 每 N 条群消息更新关系印象；0 = 关闭

# 情绪
emotion:
  max_daily_delta: 0.6             # 单日累计波动上限（M7-7 安全阀）

# 表情包收集
sticker:
  enabled: true                    # 收到图片时归档为表情包
  max_bytes: 2097152               # 单张上限，超过不入库

# 画图（工具级开关，另有群内 /draw on 二次开启）
imagegen:
  enabled: false                   # 默认关
  max_per_day: 10                  # 每人/每群每天上限


# 模型槽位：每个引用自带 base_url 与 api_key_name（→ api_key 表）。
# 能力槽位（vision/asr/tts/imagegen）的 base_url 留空 = 该能力关闭。
models:
  chat:
    # 日常闲聊，默认槽位（必填）
    fast:
      primary: { base_url: https://api.deepseek.com, model: deepseek-flash, api_key_name: deepseek }
    # 复杂推理/长文；缺失时回落 fast
    strong:
      primary: { base_url: https://api.deepseek.com, model: deepseek-v4-pro, api_key_name: deepseek }
  # 图像理解：不用时整段删除（写了就必须给 base_url/model，不支持留空关闭）
  vision:
    primary: { base_url: https://api.deepseek.com, model: deepseek-flash, api_key_name: deepseek }
  # 语音识别：base_url 留空 = 关闭；protocol: openai（默认）| qianwen
  asr:
    primary: { base_url: https://maas.qianwenaiapi.com/compatible-mode/v1, model: qwen3-asr-flash, api_key_name: asr, protocol: qianwen }
  # 语音合成：base_url 留空 = 关闭；protocol: elevenlabs（默认，pcm→silk）| breeze（mp3 直传）
  tts:
    primary: { base_url: https://api.breeze.blue/v1, model: breeze-tts-2, voice: voc_p36dbdewwvae, api_key_name: breezeblue, protocol: breeze }
  # 图像生成：base_url 留空 = 关闭；还需下面 imagegen.enabled 打开
  imagegen:
    primary: { base_url: "", model: dall-e-3, size: 1024x1024, api_key_name: openai }
  # 联网搜索/抓取端点：base_url 留空 = 默认 api.deepseek.com + 密钥 deepseek
  search:
    primary: { base_url: https://api.deepseek.com, api_key_name: deepseek, protocol: deepseek }


# 功能灰度（未列出的项按代码默认；这里是生产在用的组合）
features:
  cache: true                      # 回复缓存
  memory_extract: true             # 被动记忆抽取
  tools: true                      # 工具调用
  followup: true                   # 追问引导
  interject: true                  # 自决插话
                                   
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
