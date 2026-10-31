package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Persister 下载并落盘附件（M7-2），同时保留 Download 能力给 vision 使用。
type Persister struct {
	downloader *Downloader
	dir        string
}

// NewPersister 构造带落盘的下载器。
func NewPersister(d *Downloader, dir string) *Persister {
	if d == nil {
		d = New(nil)
	}
	return &Persister{downloader: d, dir: dir}
}

// Download 透传（不落盘，供内部使用）。
func (p *Persister) Download(ctx context.Context, url string) ([]byte, string, error) {
	return p.downloader.Download(ctx, url)
}

// Save 下载并落盘，返回字节、MIME、文件路径。
func (p *Persister) Save(ctx context.Context, url string) ([]byte, string, string, error) {
	data, mime, err := p.downloader.Download(ctx, url)
	if err != nil {
		return nil, "", "", err
	}
	path, err := p.write(data, mime)
	if err != nil {
		return nil, "", "", err
	}
	return data, mime, path, nil
}

// write 按内容哈希落盘（去重）。
func (p *Persister) write(data []byte, mime string) (string, error) {
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return "", fmt.Errorf("media: mkdir: %w", err)
	}
	sum := sha256.Sum256(data)
	path := filepath.Join(p.dir, hex.EncodeToString(sum[:16])+extOf(mime))
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("media: write: %w", err)
	}
	return path, nil
}

// extOf 按 MIME 推断扩展名。
func extOf(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/png"):
		return ".png"
	case strings.HasPrefix(mime, "image/jpeg"), strings.HasPrefix(mime, "image/jpg"):
		return ".jpg"
	case strings.HasPrefix(mime, "image/gif"):
		return ".gif"
	case strings.HasPrefix(mime, "image/webp"):
		return ".webp"
	case strings.HasPrefix(mime, "audio/"):
		return ".audio"
	default:
		return ".bin"
	}
}

// CleanupMedia 清理超龄/超量文件；返回删除数量。
func CleanupMedia(dir string, now time.Time, maxAge time.Duration, maxBytes int64) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{path: filepath.Join(dir, e.Name()), size: info.Size(), mod: info.ModTime()})
		total += info.Size()
	}
	// 先删超龄。
	for _, f := range files {
		if now.Sub(f.mod) > maxAge {
			if os.Remove(f.path) == nil {
				total -= f.size
				removed++
			}
		}
	}
	// 再按体积从旧到新删。
	for total > maxBytes && len(files) > 0 {
		f := files[0]
		files = files[1:]
		if os.Remove(f.path) == nil {
			total -= f.size
			removed++
		}
	}
	return removed, nil
}

// StartCleanup 每日清理一次（30 天 / 5GB，平台侧常量）。
func StartCleanup(ctx context.Context, dir string, clk interface{ Now() time.Time }, interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = CleanupMedia(dir, clk.Now(), 30*24*time.Hour, 5<<30)
			}
		}
	}()
}
