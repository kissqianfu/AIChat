package persona

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kissqianfu/aichat/internal/platform/hotreload"
)

// watchInterval 是人设文件轮询间隔（fsnotify 不在依赖白名单；docs/08 §5.3 要求秒级生效）。
const watchInterval = 2 * time.Second

// Store 是人设的热加载容器：文件是唯一真源，双缓冲切换，校验失败保留旧版。
//
// 语义与 docs/08 §5.3 一致：启动时加载失败拒绝启动；`/reload` 或 watch 触发 Reload，
// 失败只告警并把校验错误返回，旧版继续生效（改人设 = 前缀缓存全失效，别在高峰期反复调）。
type Store struct {
	cfg        Config
	log        *slog.Logger
	inner      *hotreload.Store[*Persona]
	watchEvery time.Duration // 测试可调小

	// baseFP 是 NewStore 加载完成那一刻的文件指纹，作为 Watch 的比对基线。
	//
	// 不能在 Watch 里现取基线：如果文件在"NewStore 读完"到"Watch 取基线"之间
	// 被改动，现取的基线会把这次改动当成初始状态，于是永远不重载——启动瞬间
	// 改人设的窗口虽小，但测试里恰好必然命中（启动 goroutine 后立刻写文件），
	// 表现为 Windows 过、Linux 挂的偶发失败。
	baseFP string
}

// NewStore 首次加载并构造热加载容器；首启失败返回错误（调用方拒绝启动）。
func NewStore(cfg Config, logger *slog.Logger) (*Store, error) {
	if logger == nil {
		logger = slog.Default()
	}
	initial, err := Load(cfg)
	if err != nil {
		return nil, err
	}
	inner, err := hotreload.New[*Persona](initial, func() (*Persona, error) { return Load(cfg) }, nil)
	if err != nil {
		return nil, err
	}
	st := &Store{cfg: cfg, log: logger, inner: inner, watchEvery: watchInterval}
	// 基线尽量贴近"刚读完文件"的时刻；stat 失败就留空，Watch 启动时再兜底取。
	if fp, err := st.fingerprint(); err == nil {
		st.baseFP = fp
	}
	return st, nil
}

// Current 返回当前生效的人设（恒非 nil）。
func (s *Store) Current() *Persona { return s.inner.Get() }

// Reload 重新加载人设文件并原子切换；失败时旧版原样可用。
func (s *Store) Reload() error {
	before := s.Current().Hash
	if err := s.inner.Reload(); err != nil {
		return err
	}
	after := s.Current()
	if after.Hash == before {
		s.log.Info("人设未变化", "version", after.Version, "hash", shortHash(after.Hash))
		return nil
	}
	s.log.Info("人设已重载", "version", after.Version, "hash", shortHash(after.Hash), "bytes", len(after.Body))
	return nil
}

// Watch 轮询人设文件：内容指纹变化就 Reload；ctx 取消退出。失败只告警，旧版继续生效。
func (s *Store) Watch(ctx context.Context) {
	interval := s.watchEvery
	if interval <= 0 {
		interval = watchInterval
	}
	// 基线用 NewStore 时记下的指纹；拿不到（当时 stat 失败）才现取一次。
	last := s.baseFP
	if last == "" {
		fp, err := s.fingerprint()
		if err != nil {
			s.log.Warn("人设 watch 初始化失败，等文件就绪", "err", err)
		}
		last = fp
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := s.fingerprint()
			if err != nil {
				continue // 文件可能正被替换（临时缺失），下一轮再看
			}
			if next == last {
				continue
			}
			last = next
			if err := s.Reload(); err != nil {
				s.log.Warn("人设热加载失败，保留旧版", "err", err)
			}
		}
	}
}

// fingerprint 用"文件 mtime + 大小"表示人设文件是否变化（不读全文，代价极低）。
func (s *Store) fingerprint() (string, error) {
	var b strings.Builder
	for _, name := range []string{s.cfg.FileText, s.cfg.FileMeta} {
		info, err := os.Stat(filepath.Join(s.cfg.Dir, name))
		if err != nil {
			return "", fmt.Errorf("persona: stat %s: %w", name, err)
		}
		fmt.Fprintf(&b, "%s:%d:%d;", name, info.ModTime().UnixNano(), info.Size())
	}
	return b.String(), nil
}

// shortHash 取哈希前 12 位用于日志/审计（空哈希回退 "?"）。
func shortHash(hash string) string {
	if len(hash) < 12 {
		return hash
	}
	return hash[:12]
}
