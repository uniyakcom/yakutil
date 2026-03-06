#!/usr/bin/env bash
# lint.sh — 代码质量检查脚本
#
# 用法:
#   ./lint.sh            # 完整检查（gofmt -s -w + vet + golangci-lint）
#   ./lint.sh --vet      # gofmt -s -w + go vet（跳过 golangci-lint）
#   ./lint.sh --fix      # gofmt -s -w + vet + golangci-lint --fix
#   ./lint.sh --fmt      # 仅格式化（gofmt -s -w），不运行 vet/lint
#   ./lint.sh --test     # 快速测试（go test ./... -race -count=1）
#
# 注：gofmt -s -w 在所有非 --test / --fmt 路径中均自动执行。
#     -s 会简化冗余的复合字面量、切片表达式等写法。

set -euo pipefail

cd "$(dirname "$0")"

# 确保 GOPATH/bin 在 PATH 中（golangci-lint 安装位置）
export PATH="$(go env GOPATH)/bin:$PATH"

VET=false
FIX=false
FMT=false
TEST=false

for arg in "$@"; do
  case $arg in
    --vet) VET=true ;;
    --fix)      FIX=true ;;
    --fmt)      FMT=true ;;
    --test)     TEST=true ;;
    *) echo "unknown arg: $arg"; exit 1 ;;
  esac
done

# ─── --fmt: 格式化所有 Go 文件 ──────────────────────────────────────────────

if $FMT; then
  echo "==> gofmt -s -w ."
  gofmt -s -w .
  echo "    ✓ 格式化完成"
  exit 0
fi

# ─── --test: 快速测试 ────────────────────────────────────────────────────────

if $TEST; then
  echo "==> go test ./... -race -count=1 -timeout=120s"
  go test ./... -race -count=1 -timeout=120s
  echo "    ✓ 全部测试通过"
  exit 0
fi

# ─── gofmt -s -w（默认始终执行）──────────────────────────────────────────────

echo "==> gofmt -s -w ."
gofmt -s -w .
echo "    ✓ 格式化完成"

# ─── go vet ─────────────────────────────────────────────────────────────────

echo "==> go vet ./..."
go vet ./...
echo "    ✓ go vet passed (0 issues)"

if $VET; then
  exit 0
fi

# ─── golangci-lint ──────────────────────────────────────────────────────────

if ! command -v golangci-lint &>/dev/null; then
  echo "==> golangci-lint not found, installing latest via official install script..."
  curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(go env GOPATH)/bin"
fi

FIX_FLAG=""
if $FIX; then
  FIX_FLAG="--fix"
fi

echo "==> golangci-lint run ./... ${FIX_FLAG}"
golangci-lint run ./... $FIX_FLAG
echo "    ✓ golangci-lint passed"
