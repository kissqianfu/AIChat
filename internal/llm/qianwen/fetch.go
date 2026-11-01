// fetch.go：千问系产物下载的公共助手。
//
// 文生图与语音合成的产物形态相同：官方只给一个 OSS 临时地址（24h 过期），
// 必须当场 GET 落地。校验规则也一致——限读上限防异常超大响应、空体报错——
// 所以收拢成一个函数，差异（体积上限、产物名、MIME 兜底）留给调用方。
package qianwen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// fetchArtifact 下载产物地址，返回内容与 Content-Type（原样，兜底由调用方定）。
func fetchArtifact(ctx context.Context, client *http.Client, url string, max int64, what string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("qianwen: 构造%s下载: %w", what, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("qianwen: 下载%s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("qianwen: 下载%s HTTP %d", what, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max))
	if err != nil {
		return nil, "", fmt.Errorf("qianwen: 读%s: %w", what, err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("qianwen: %s内容为空", what)
	}
	return data, strings.TrimSpace(resp.Header.Get("Content-Type")), nil
}
