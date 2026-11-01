#!/usr/bin/env bash
# aichat 的四道闸（docs/10 §7）：G1 可编译 → G2 无重复 → G3 无漏洞 → G4 无 BUG。
#
# 顺序由 set -e 固化：上一道不过，后面根本不会跑。工具没装 = 失败，不是跳过。
#
# 前置工具：
#   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
#   go install github.com/mibk/dupl@latest
#   go install golang.org/x/vuln/cmd/govulncheck@latest
#   go install github.com/securego/gosec/v2/cmd/gosec@latest
#
# G4 的 go test -race 需要 CGO + gcc：Windows 上装 MinGW-w64（如 winlibs 便携版）
# 并把它的 bin 目录加入 PATH；其余步骤不需要 CGO。
#
# 用法：bash scripts/check.sh [--skip-g4]
#   --skip-g4 只在你明确知道"这次不算检查"时用；PR/提交里的四行结论必须写"未检查"。

set -euo pipefail

cd "$(dirname "$0")/.."

readonly MODULE="github.com/kissqianfu/aichat"

SKIP_G4=0
for arg in "$@"; do
	case "$arg" in
		--skip-g4) SKIP_G4=1 ;;
		*)
			printf '未知参数：%s\n' "$arg" >&2
			exit 2
			;;
	esac
done

step() { printf '\n\033[1m=== %s ===\033[0m\n' "$1"; }
need() {
	command -v "$1" >/dev/null 2>&1 || {
		printf '缺失工具：%s（工具没装算失败，不算跳过）\n' "$1" >&2
		exit 1
	}
}

# ---------------------------------------------------------------------------
step "G1 可编译"
need go
need gofmt

unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
	printf 'gofmt 未格式化（先跑 gofmt -w）：\n%s\n' "$unformatted" >&2
	exit 1
fi
go build ./...
go vet ./...

# 交叉编译验的是「不依赖本机平台」，Linux CI 与 Windows 本地都得过。
# CGO_ENABLED=0 必须显式写死：G4 的 -race 要求 CGO=1（见文件头），而 CGO=1 时
# 拿宿主 gcc 去编 Windows 目标会直接挂在 runtime/cgo（grp.h: No such file）。
# 本地是 Windows，GOOS=windows 只是本地编译所以察觉不到，CI 上是 Linux 必挂。
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...

# ---------------------------------------------------------------------------
step "G2 无重复"
need dupl
need golangci-lint

# 输出供人看、不阻断：dupl 报的重复不一定都该抽（docs/10 §10），但必须有人回应。
# 注意：dupl 只认目录/文件，不认 Go 的 ./... 通配（传 ./... 会当成路径走进空目录并崩溃）。
dupl -threshold 100 . || true
golangci-lint run --enable-only dupl,depguard,forbidigo

# 本项目专属 grep：命中即说明漏了一层（docs/10 §7.4）
FAIL_GREP=0
check_grep() {
	local name="$1" pattern="$2" allow="$3" found
	found="$(grep -rnE --include='*.go' "$pattern" internal/ 2>/dev/null | grep -vE "$allow" || true)"
	if [ -n "$found" ]; then
		printf '❌ %s\n%s\n' "$name" "$found" >&2
		FAIL_GREP=1
	fi
}
check_grep "msg_type 只允许出现在 render / outbound" 'msg_type' '^internal/(render|outbound)/|_test\.go:'
check_grep "SQL 只允许出现在 storage/db（repo 与迁移器）" 'SELECT |INSERT INTO |UPDATE .* SET |DELETE FROM |VACUUM |ATTACH |PRAGMA ' '^internal/storage/db/'
check_grep "裸 time.Now() 只允许出现在 platform/clock" '\btime\.Now\(\)' '^internal/platform/clock/'
check_grep "自建 http.Client 应走统一 client" '&http\.Client\{' '^internal/platform/httpclient/|_test\.go:'
check_grep "手拼 JSON 应走 struct + json.Marshal" 'fmt\.Sprintf\("\{' '^$'
if [ "$FAIL_GREP" -ne 0 ]; then
	printf '\nG2 失败：上面每处命中都要给出"该抽 / 不该抽（写理由）/ 进 docs/debt.md"三选一结论。\n' >&2
	exit 1
fi

# ---------------------------------------------------------------------------
step "G3 无漏洞"
need govulncheck
need gosec

govulncheck ./...
gosec -severity high -quiet ./...

# 直接依赖目标 ≤ 5（docs/04 §4）。用 go list -m 过滤 Main/Indirect，
# 不用 go mod graph（它会把 `go@1.27.0` 伪模块也算一条），也不用 go list -m all（含传递依赖）。
direct="$(go list -m -f '{{if not .Main}}{{if not .Indirect}}{{.Path}}{{end}}{{end}}' all | sed '/^$/d' | wc -l | tr -d ' ')"
printf '直接依赖：%s 个（目标 ≤ 5）\n' "$direct"
if [ "$direct" -gt 5 ]; then
	printf '直接依赖 %s 个，超出白名单目标（docs/00 §5）\n' "$direct" >&2
	exit 1
fi

# ---------------------------------------------------------------------------
step "G4 无 BUG"
if [ "$SKIP_G4" -eq 1 ]; then
	printf '（已按 --skip-g4 跳过：四行结论里必须写"未检查"）\n' >&2
else
	need go

	# 覆盖率按 docs/10 §6 分级；目录还没有 .go 文件时跳过（尚未落地）。
	check_cover() {
		local dir="$1" min="$2" profile cov
		if ! find "$dir" -name '*.go' -print -quit | grep -q .; then
			printf '（跳过 %s：暂无 .go 文件）\n' "$dir"
			return 0
		fi
		profile="cover.base.out"
		go test -count=1 -coverprofile="$profile" "./${dir}/..." >/dev/null
		cov="$(go tool cover -func="$profile" | tail -n 1 | awk '{print $3}' | tr -d '%')"
		rm -f "$profile"
		awk -v c="$cov" -v m="$min" -v d="$dir" 'BEGIN {
			if (c + 0 < m + 0) {
				printf "❌ 覆盖率不足：%s = %s%%，门槛 %s%%\n", d, c, m
				exit 1
			}
			printf "覆盖率：%s = %s%%（门槛 %s%%）\n", d, c, m
		}'
	}

	go test -race -count=1 ./...
	go test -count=1 ./... >/dev/null
	check_cover internal/platform 90
	check_cover internal/render 80
	check_cover internal/schedule 80
fi

printf '\n\033[1m四道闸全部通过（G1 ✅ G2 ✅ G3 ✅ G4 ✅）\033[0m\n'


