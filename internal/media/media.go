// Package media 负责下载平台附件（图片/语音），供 vision/ASR 使用。
package media

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/httpclient"
)

const (
	// MaxBytes 是单个附件的最大体积（8MB）。
	MaxBytes = 8 << 20
	// userAgent 下载附件时的 UA。
	userAgent = "aichat/1.0"
)

// Downloader 按 URL 下载附件。
type Downloader struct {
	http          *http.Client
	allowInsecure bool // 测试用：允许 http
}

// New 构造下载器。
func New(client *http.Client) *Downloader {
	if client == nil {
		client = httpclient.New(15 * time.Second)
	}
	return &Downloader{http: client}
}

// NewAllowInsecure 是测试构造函数（允许 http）。
func NewAllowInsecure(client *http.Client) *Downloader {
	d := New(client)
	d.allowInsecure = true
	return d
}

// Download 下载并返回字节与 MIME；只允许 https（测试可放宽），超 8MB 拒绝。
func (d *Downloader) Download(ctx context.Context, rawURL string) ([]byte, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return nil, "", fmt.Errorf("media: 非法 URL %q", rawURL)
	}
	if parsed.Scheme != "https" && !(d.allowInsecure && parsed.Scheme == "http") {
		return nil, "", fmt.Errorf("media: 只允许 https，收到 %q", parsed.Scheme)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("media: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("media: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("media: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("media: read: %w", err)
	}
	if len(data) > MaxBytes {
		return nil, "", errors.New("media: 附件超过 8MB")
	}
	if len(data) == 0 {
		return nil, "", errors.New("media: 空附件")
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" || strings.HasPrefix(mime, "application/octet-stream") {
		mime = http.DetectContentType(data)
	}
	return data, mime, nil
}

// DataURL 把字节转成 data URL（给 vision 模型用，避免依赖临时 URL）。
func DataURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}
