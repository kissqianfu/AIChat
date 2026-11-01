// imagegen.go：千问文生图（Qwen-Image），走百炼**原生** multimodal-generation。
//
// 为什么不用 OpenAI 兼容模式（2026-11-01 实测）：qwen-image 系列不在 compatible-mode
// 下——POST /compatible-mode/v1/images/generations 直接 404，看起来像"接口挂了"。
// 原生端点是
//
//	POST {root}/api/v1/services/aigc/multimodal-generation/generation
//
// 请求体是 input.messages[*].content[*].text（不是 prompt 字段），响应给的是
// **图片 URL**（有效期 24 小时，不支持 b64_json）——所以这里必须当场下载成字节。
// 把 URL 往上传等于让图片在 24 小时后凭空失效，而调用方（QQ 发图）根本没法补救。
package qianwen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/httpclient"
)

const (
	// DefaultImageBaseURL 是千问 AI 平台（MaaS 网关）的根域名
	// （原生接口，注意不带 /compatible-mode/v1）。
	DefaultImageBaseURL = "https://maas.qianwenaiapi.com"
	// DefaultImageModel 是缺省文生图模型。qwen-image-2.0 是加速版，
	// pro 版更贵且只在文字渲染上更好，闲聊画图用不上。
	DefaultImageModel = "qwen-image-2.0"
	// imageGenPath 是原生多模态生成路径（换新域名也还是这个 path）。
	imageGenPath = "/api/v1/services/aigc/multimodal-generation/generation"
	// defaultImageSize 是缺省出图尺寸（百炼默认 2048*2048，出图慢且贵，这里收小）。
	defaultImageSize = "1024*1024"
	// imageMaxBytes 限制下载的图片体积（生成图一般 < 4MiB，给足富余）。
	imageMaxBytes = 16 << 20
	// imageGenTimeout 是单次生成的整体超时：官方实测 2~8 秒，冷门提示词会慢些。
	imageGenTimeout = 90 * time.Second
)

// ImageConfig 是文生图参数；APIKey 用回调是为了支持密钥轮换（每次调用时取）。
type ImageConfig struct {
	APIKey  func(ctx context.Context) (string, error)
	BaseURL string
	Model   string
	Size    string
	HTTP    *http.Client
}

// NewImageGen 构造文生图闭包，签名与 tools.ImageGenFn 一致。
func NewImageGen(cfg ImageConfig) func(ctx context.Context, prompt string) ([]byte, string, error) {
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultImageModel
	}
	size := NormalizeImageSize(cfg.Size)
	endpoint := ImageEndpoint(cfg.BaseURL)
	client := cfg.HTTP
	if client == nil {
		client = httpclient.New(imageGenTimeout)
	}
	return func(ctx context.Context, prompt string) ([]byte, string, error) {
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			return nil, "", errors.New("qianwen: 提示词为空")
		}
		if cfg.APIKey == nil {
			return nil, "", errors.New("qianwen: 未配置 APIKey 回调")
		}
		key, err := cfg.APIKey(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("qianwen: 取密钥: %w", err)
		}

		// 官方限制：messages 仅支持单轮且只有一个元素，content 里只能有一个 text。
		body := map[string]any{
			"model": model,
			"input": map[string]any{
				"messages": []any{map[string]any{
					"role":    "user",
					"content": []any{map[string]any{"text": prompt}},
				}},
			},
			"parameters": map[string]any{
				"size":          size,
				"n":             1,
				"watermark":     false, // 右下角 "Qwen-Image" 水印，默认关
				"prompt_extend": true,  // 提示词智能改写：闲聊画图吃亏在描述短
			},
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("qianwen: 请求文生图: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return nil, "", fmt.Errorf("qianwen: 读响应: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, "", apiError("文生图", resp.StatusCode, data)
		}

		var parsed struct {
			Output struct {
				Choices []struct {
					Message struct {
						Content []struct {
							Image string `json:"image"`
							Text  string `json:"text"`
						} `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			} `json:"output"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, "", fmt.Errorf("qianwen: 解析文生图响应: %w", err)
		}
		// 内容数组里可能先给一段 text 再给 image，所以要找第一个非空 image，
		// 不能取 content[0]——顺序由模型决定，不保证。
		imageURL := ""
		for _, choice := range parsed.Output.Choices {
			for _, item := range choice.Message.Content {
				if item.Image != "" {
					imageURL = item.Image
					break
				}
			}
			if imageURL != "" {
				break
			}
		}
		if imageURL == "" {
			return nil, "", fmt.Errorf("qianwen: 文生图响应没有图片（request_id=%s）", parsed.RequestID)
		}
		return downloadImage(ctx, client, imageURL)
	}
}

// ImageEndpoint 拼文生图端点：把「兼容模式 / 裸域名 / 完整端点」统一成原生路径。
//
// 配置里写 base_url 的三种常见写法都要能接住：
//   - https://dashscope.aliyuncs.com                （裸域名）
//   - https://dashscope.aliyuncs.com/compatible-mode/v1  （照抄对话槽位的那行）
//   - https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com （业务空间专属域名）
//
// 已经是完整端点的原样返回——用户既然写全了，就别再动。
func ImageEndpoint(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return DefaultImageBaseURL + imageGenPath
	}
	base = strings.TrimRight(base, "/")
	if strings.Contains(base, "/services/aigc/") {
		return base
	}
	base = strings.TrimSuffix(base, "/compatible-mode/v1")
	base = strings.TrimSuffix(base, "/api/v1")
	return base + imageGenPath
}

// NormalizeImageSize 把 size 统一成百炼要的「宽*高」。
//
// 配置里习惯写 1024x1024（OpenAI 风格），官方要 1024*1024；写错格式不会报错，
// 只会被当成非法值退到默认尺寸——那种"设了但没生效"最难查，所以在本地就换掉。
func NormalizeImageSize(size string) string {
	size = strings.TrimSpace(size)
	if size == "" {
		return defaultImageSize
	}
	parts := strings.FieldsFunc(size, func(r rune) bool {
		return r == 'x' || r == 'X' || r == '*' || r == '×'
	})
	if len(parts) != 2 {
		return defaultImageSize
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return defaultImageSize
	}
	return fmt.Sprintf("%d*%d", w, h)
}

// apiError 把官方错误体拼成人能看的一句话（带 code 与 request_id，便于提工单）。
func apiError(what string, status int, body []byte) error {
	var e struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &e); err != nil || e.Code == "" {
		text := string(body)
		if len(text) > 200 {
			text = text[:200]
		}
		return fmt.Errorf("qianwen: %s HTTP %d: %s", what, status, text)
	}
	return fmt.Errorf("qianwen: %s失败 [%s] %s（request_id=%s）", what, e.Code, e.Message, e.RequestID)
}

// downloadImage 取回生成结果。官方只给 URL 且 24 小时过期，所以必须当场落地。
func downloadImage(ctx context.Context, client *http.Client, imageURL string) ([]byte, string, error) {
	if !strings.HasPrefix(imageURL, "http://") && !strings.HasPrefix(imageURL, "https://") {
		return nil, "", fmt.Errorf("qianwen: 图片地址不是 URL: %q", imageURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("qianwen: 下载生成图: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("qianwen: 下载生成图 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, imageMaxBytes))
	if err != nil {
		return nil, "", fmt.Errorf("qianwen: 读图片: %w", err)
	}
	if len(data) == 0 {
		return nil, "", errors.New("qianwen: 图片内容为空")
	}
	// OSS 返回的 Content-Type 可靠；取不到时按 png（官方固定输出 png）。
	mime := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/png"
	}
	return data, mime, nil
}
