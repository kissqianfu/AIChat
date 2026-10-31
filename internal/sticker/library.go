// Package sticker 是本地表情包库（M3-4）：扫描目录、按名字查找/随机取一张。
//
// 发送图片走 qqapi 的 IntentMedia（需要公网可访问 URL）；M3 先提供库与选择，
// 公网图床/反代静态目录在 M4 部署时接上。
package sticker

import (
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry 是一个表情包文件。
type Entry struct {
	Name string
	Path string
}

// Library 是加载后的表情包库。
type Library struct {
	entries []Entry
}

// Load 扫描目录（png/jpg/jpeg/gif/webp），文件名（去扩展名）即名字。
func Load(dir string) (*Library, error) {
	if dir == "" {
		return nil, errors.New("sticker: dir 不能为空")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(files))
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name()))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		default:
			continue
		}
		entries = append(entries, Entry{
			Name: strings.TrimSuffix(f.Name(), filepath.Ext(f.Name())),
			Path: filepath.Join(dir, f.Name()),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return &Library{entries: entries}, nil
}

// List 返回全部条目（副本）。
func (l *Library) List() []Entry {
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Lookup 按名字精确查找（大小写不敏感）。
func (l *Library) Lookup(name string) (Entry, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, e := range l.entries {
		if strings.ToLower(e.Name) == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Random 随机取一张（空库返回 false）。
func (l *Library) Random() (Entry, bool) {
	if len(l.entries) == 0 {
		return Entry{}, false
	}
	// #nosec G404 -- 随机选表情包不需要密码学随机
	return l.entries[rand.IntN(len(l.entries))], true
}

// URL 拼公网地址（publicBase 形如 https://example.com/stickers/）。
func URL(publicBase string, e Entry) string {
	base := strings.TrimRight(publicBase, "/")
	return base + "/" + filepath.Base(e.Path)
}
