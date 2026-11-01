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

# 账户余额：只读展示「还剩多少」，以及余额曲线下降的斜率＝真实消耗速度。
# 它不维护单价表，所以不会算错；对账看它，估算看 aichat_billing_amount_total。
# 一家一个账户：DeepSeek 是人民币，Breeze 是积分，单位不同，看板分行、不能相加。
balance:
  enabled: true                    # 关掉就不发这些请求
  interval_minutes: 10             # 每 N 分钟查一次（下限 1）
  accounts:
    - provider: deepseek           # 必填：deepseek / breeze（决定用哪家的余额接口）
      base_url: ""                 # 留空 = 该 provider 的默认端点
      api_key_name: deepseek       # → api_key 表，必填
    # - provider: breeze           # 积分账户（GET /v1/balance，xi-api-key 认证）
    #   base_url: https://api.breeze.blue/v1
    #   api_key_name: breezeblue


# 模型槽位：每个引用自带 protocol / model / api_key_name（→ api_key 表）。
#   protocol 决定「用谁家的接口」，连哪个地址也由它决定——端点写在各家自己的
#   模块里（internal/llm/<vendor>），所以配置里**不需要 base_url**；只有走代理
#   或自建网关时才在槽位里覆盖它。
#     对话/视觉  deepseek（默认）| openai | qianwen
#     asr        openai（默认）| qianwen
#     tts        elevenlabs（默认）| breeze
#     imagegen   openai（默认，/images/generations）| qianwen（百炼原生接口）
#     search     deepseek
#   **关掉某个能力 = 不写这个槽位**（整段删掉或注释掉），不是把 base_url 写空。
#   只有模型名不校验——写错的 model 要到调用时才暴露。
models:
  chat:
    # 日常闲聊，默认槽位（必填）。protocol 省略 = deepseek。
    fast:
      primary: { protocol: deepseek, model: deepseek-flash, api_key_name: deepseek }
    # 复杂推理/长文；缺失时回落 fast
    strong:
      primary: { protocol: deepseek, model: deepseek-v4-pro, api_key_name: deepseek }
  # 图像理解：不用时整段删除
  vision:
    primary: { protocol: deepseek, model: deepseek-flash, api_key_name: deepseek }
  # 语音识别：不用时不写这段（写了就要有 model 和存在的 key）。启用示例：
  #   asr:
  #     primary: { protocol: qianwen, model: qwen3-asr-flash, api_key_name: qianwen }
  # 语音合成：elevenlabs（pcm→silk）| breeze（mp3 直传，免 silk 编码器）
  tts:
    primary: { protocol: breeze, model: breeze-tts-2, voice: voc_p36dbdewwvae, api_key_name: breezeblue }
  # 图像生成：还需上面 imagegen.enabled 打开。protocol 决定接口形态——
  #   qianwen：model 写 qwen-image-2.0（不是 Qwen-Image-3.0 之类），出图默认 1024*1024
  #   openai ：model 写 dall-e-3 / gpt-image-1
  # size 用 1024x1024 写法即可，千问侧会自动换成 1024*1024。
  imagegen:
    primary: { protocol: openai, model: dall-e-3, size: 1024x1024, api_key_name: openai }
  # 联网搜索/抓取：deepseek（默认端点 api.deepseek.com）
  search:
    primary: { protocol: deepseek, api_key_name: deepseek }


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
