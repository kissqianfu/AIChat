# aichat

QQ 官方机器人 × 多模态大模型的 Go 单二进制实现：单进程、单机器人、单实例，**不用 Redis**（内存 LRU + 令牌桶 + SQLite 替代）。

- **设计文档是唯一真源**：`docs/00` ~ `docs/10`。开工前至少读 `docs/00`、`docs/10`、`docs/02`。
- **开发约定**：`DEV_PROMPT.md`（给 AI / 人的系统提示词：硬基线、四道闸、已经翻过的车）。
- **当前进度**：M0-1 骨架 —— `go.mod` + `internal/platform/clock,ids` + `scripts/check.sh` 四道闸跑通。下一任务见 `docs/03 §5`。

## 仓库约定

| 项 | 取值 | 理由 |
|---|---|---|
| module | `github.com/kissqianfu/aichat` | 已拍板；import 一律用完整路径 |
| 接入层 SDK | **独立仓库** `QQ-SDK`（`github.com/kissqianfu/QQ-SDK`）；本地开发放在 `../QQ-SDK`，M0-12 接线时用 `require` + `replace` | 零业务依赖，可拆卸（`docs/04`） |
| 二进制 / 数据 | `./aichat` / `./data/aichat.db` | SQLite WAL，纯 Go 驱动免 CGO |
| 人设 | `persona/persona.md` + `persona.yaml`，**DB 不存** | `docs/08 §5.2` |

## 构建与检查

```bash
bash scripts/check.sh          # 四道闸：G1 可编译 → G2 无重复 → G3 无漏洞 → G4 无 BUG
bash scripts/check.sh --skip-g4   # 仅调试用；四行结论里必须写"未检查"
```

前置工具（缺一个 check.sh 即失败，不会跳过）：

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
go install github.com/mibk/dupl@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest
```

- Windows 上用 **Git Bash** 执行（`set -euo pipefail`、ANSI 色、`cd ../QQ-SDK` 都是 bash 语义）。
- G4 的 `go test -race` 需要 CGO + gcc：装 MinGW-w64（如 winlibs 便携版）并把 `bin` 加进 `PATH`；其余部分**不需要 CGO**（`modernc.org/sqlite` 纯 Go）。
- 不部署云服务器：本期只本地开发与验证。

## 目录

完整目录树见 `docs/02 §3`。本仓库已按该树建好骨架（空目录用 `.gitkeep` 占位），
`internal/platform/` 的八个公共内核只允许有一份实现（`docs/10 §3`），逐个在 M0 落地。
