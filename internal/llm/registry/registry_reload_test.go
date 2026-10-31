package registry

import (
	"fmt"
	"os"
	"testing"
)

// modelsYAML 生成一份最小合法 config.yaml models 段（可替换 chat.fast 的模型名）。
func modelsYAML(model string) string {
	return `models:
  chat:
    fast: {primary: {base_url: https://api.deepseek.com, model: ` + model + `, api_key_name: k}}
`
}

// reloadWith 重写文件并 Reload。
func reloadWith(t *testing.T, r *Registry, path, model string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(modelsYAML(model)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
}

func TestRegistryReloadAndRollback(t *testing.T) {
	path := writeTemp(t, modelsYAML("model-1"))
	r, err := Load(path, Options{Key: testKey, SnapshotKeep: 3})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, _ := r.SlotModel("chat.fast"); got.Model != "model-1" {
		t.Fatalf("初始模型 = %q", got.Model)
	}
	reloadWith(t, r, path, "model-2")
	if got, _ := r.SlotModel("chat.fast"); got.Model != "model-2" {
		t.Fatalf("热加载后模型 = %q", got.Model)
	}
	if r.SnapshotCount() != 1 {
		t.Fatalf("SnapshotCount() = %d", r.SnapshotCount())
	}
	// 回滚到 model-1。
	if err := r.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if got, _ := r.SlotModel("chat.fast"); got.Model != "model-1" {
		t.Fatalf("回滚后模型 = %q", got.Model)
	}
	if r.SnapshotCount() != 0 {
		t.Fatalf("回滚后 SnapshotCount() = %d", r.SnapshotCount())
	}
	// 没有快照再回滚 → 错误。
	if err := r.Rollback(); err == nil {
		t.Fatal("Rollback(empty) error = nil")
	}
}

func TestRegistryReloadInvalidKeepsOld(t *testing.T) {
	path := writeTemp(t, modelsYAML("model-1"))
	r, err := Load(path, Options{Key: testKey})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// 非法段（chat 槽位缺 base_url）→ 校验失败，保留旧版。
	invalid := "models:\n  chat:\n    fast: {primary: {base_url: \"\", model: m}}\n"
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(); err == nil {
		t.Fatal("Reload(invalid) error = nil")
	}
	if got, _ := r.SlotModel("chat.fast"); got.Model != "model-1" {
		t.Fatalf("失败后未保留旧版: %q", got.Model)
	}
	if r.SnapshotCount() != 0 {
		t.Fatalf("失败不应产生快照: %d", r.SnapshotCount())
	}
}

func TestRegistrySnapshotKeepCap(t *testing.T) {
	path := writeTemp(t, modelsYAML("m0"))
	r, err := Load(path, Options{Key: testKey, SnapshotKeep: 2})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for i := 1; i <= 3; i++ {
		reloadWith(t, r, path, fmt.Sprintf("m%d", i))
	}
	if r.SnapshotCount() != 2 {
		t.Fatalf("SnapshotKeep=2 时 SnapshotCount() = %d", r.SnapshotCount())
	}
}
