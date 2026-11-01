// tts.go：OpenAI 标准语音合成（POST /audio/speech，返回音频字节）。
//
// 目前**没有**登记成 tts 协议：生产用的是 elevenlabs / breeze（见各自包的 tts.go），
// 这条链路没有在真实端点上验证过，登记了就等于给配置开了个没人测过的口子。
// 需要时把它接上即可——Client.Speech 的签名与 tts.Synth 一致。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Speech 文本转语音（/audio/speech），返回音频字节与 MIME。
func (c *Client) Speech(ctx context.Context, model, voice, text string) ([]byte, string, error) {
	reqBody, err := json.Marshal(map[string]any{"model": model, "voice": voice, "input": text, "response_format": "mp3"})
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/speech", bytes.NewReader(reqBody))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	data, status, header, err := c.send(req, 16<<20)
	if err != nil {
		return nil, "", err
	}
	if status != http.StatusOK {
		return nil, "", fmt.Errorf("openai: TTS HTTP %d: %s", status, truncate(string(data), 200))
	}
	mime := header.Get("Content-Type")
	if mime == "" {
		mime = "audio/mpeg"
	}
	return data, mime, nil
}
