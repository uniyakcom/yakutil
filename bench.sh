#!/bin/bash
# yakutil 基准测试脚本
# 用法: ./bench.sh [benchtime] [count]
#   benchtime: 每次测试持续时间（默认 3s）
#   count:     重复次数（默认 3）

set -e

BENCHTIME="${1:-3s}"
COUNT="${2:-3}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# 获取系统信息
TIMESTAMP="$(date '+%Y-%m-%d %H:%M:%S %Z')"
KERNEL="$(uname -srm)"
GOVERSION="$(go version | awk '{print $3}')"
CPU="$(grep 'model name' /proc/cpuinfo 2>/dev/null | head -1 | sed 's/.*: //' || sysctl -n machdep.cpu.brand_string 2>/dev/null || echo 'unknown')"

# 生成输出文件名: bench_<os>_<cores>c<threads>t.txt
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
    mingw*|msys*|cygwin*) OS="windows" ;;
esac
CORES="$(grep '^core id' /proc/cpuinfo 2>/dev/null | sort -u | wc -l || sysctl -n hw.physicalcpu 2>/dev/null || nproc --all 2>/dev/null || echo '?')"
THREADS="$(grep -c '^processor' /proc/cpuinfo 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo "$CORES")"
OUTFILE="$SCRIPT_DIR/bench_${OS}_${CORES}c${THREADS}t.txt"

echo "=== yakutil benchmark ==="
echo "  benchtime: $BENCHTIME"
echo "  count:     $COUNT"
echo "  output:    $OUTFILE"
echo ""

# 写入头部
{
    echo "# $TIMESTAMP"
    echo "# $KERNEL"
    echo "# $GOVERSION"
    echo "# CPU: $CPU"
    echo ""
    echo "goos: $OS"
    echo "goarch: $(go env GOARCH)"
    echo "pkg: github.com/uniyakcom/yakutil"
    echo "cpu: $CPU"
    echo ""
} > "$OUTFILE"

# 运行基准测试（覆盖所有子包）
echo "Running benchmarks..."
cd "$SCRIPT_DIR"
go test ./... -bench=. -benchmem -benchtime="$BENCHTIME" -count="$COUNT" -run='^$' 2>&1 | tee /dev/stderr | grep '^Benchmark' >> "$OUTFILE"

# 写入尾部
{
    echo ""
    echo "PASS"
} >> "$OUTFILE"

echo ""
echo "=== Results saved to $OUTFILE ==="
