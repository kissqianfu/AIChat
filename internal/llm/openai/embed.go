package openai

import (
	"context"
	"encoding/json"
	"fmt"
)

// Embed 生成文本向量（/embeddings，批量；OpenAI 兼容端点通用）。
// 供记忆向量回填与召回 query 向量化使用（models.embed 槽位）。
func (c *Client) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	raw, err := c.do(ctx, "/embeddings", map[string]any{"model": model, "input": texts})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("openai: decode embeddings: %w", err)
	}
	out := make([][]float32, len(texts))
	for _, item := range resp.Data {
		if item.Index >= 0 && item.Index < len(out) {
			out[item.Index] = item.Embedding
		}
	}
	for i, vec := range out {
		if len(vec) == 0 {
			return nil, fmt.Errorf("openai: embeddings[%d] 为空", i)
		}
	}
	return out, nil
}
