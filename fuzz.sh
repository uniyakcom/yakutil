#!/usr/bin/env bash
# fuzz.sh — 本地逐目标 Fuzz + 崩溃自动检测
#
# yakutil 的 Fuzz 目标分布在多个子包下，用 pkg/target 格式标识。
#
# 用法：
#   ./fuzz.sh                              全部目标，每个 1m
#   ./fuzz.sh 2m                           全部目标，自定义时长
#   ./fuzz.sh ring/FuzzWriteRead           单目标（pkg/target），1m
#   ./fuzz.sh ring/FuzzWriteRead 2m        单目标，自定义时长
#   ./fuzz.sh FuzzWriteRead                按目标名搜索（跨包），1m
#   FUZZ_TIME=10m ./fuzz.sh                环境变量指定时长
#
# Ctrl+C：中断当前目标并继续下一个；再按一次退出整个脚本。
# 日志：所有输出（含崩溃信息）自动保存到 fuzz_logs/fuzz_<时间戳>.log

set -uo pipefail

# ── 含有 Fuzz 目标的子包列表 ─────────────────────────────────────────────────
FUZZ_PKGS=(ring hll ratelimit art lru sketch)

# ── 自动发现所有 Fuzz 目标，以 "pkg/target" 格式存储 ─────────────────────────
# go test -list 尊重 build tag，结果比 grep 更准确
ALL_TARGETS=()
for pkg in "${FUZZ_PKGS[@]}"; do
  while IFS= read -r fn; do
    ALL_TARGETS+=("${pkg}/${fn}")
  done < <(go test -list '^Fuzz' "./${pkg}" 2>/dev/null | grep '^Fuzz')
done

if [[ ${#ALL_TARGETS[@]} -eq 0 ]]; then
  echo "未发现任何 Fuzz 测试函数，退出。" >&2
  exit 1
fi

# ── 参数解析 ──────────────────────────────────────────────────────────────────
FUZZ_TIME="${FUZZ_TIME:-1m}"
TARGETS=()

for arg in "$@"; do
  case "$arg" in
    # pkg/target 格式
    */Fuzz*)
      TARGETS+=("$arg") ;;
    # 裸 target 名（跨包搜索）
    Fuzz*)
      found=()
      for pt in "${ALL_TARGETS[@]}"; do
        [[ "${pt##*/}" == "$arg" ]] && found+=("$pt")
      done
      if [[ ${#found[@]} -eq 0 ]]; then
        echo "未找到目标 '$arg'  （可用目标：${ALL_TARGETS[*]}）" >&2; exit 1
      elif [[ ${#found[@]} -gt 1 ]]; then
        echo "目标 '$arg' 存在于多个包：${found[*]}，请用 pkg/target 格式指定" >&2; exit 1
      fi
      TARGETS+=("${found[0]}") ;;
    *[0-9][smh]) FUZZ_TIME="$arg" ;;
    -h|--help)
      sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) echo "未知参数: $arg  （运行 $0 --help 查看用法）" >&2; exit 1 ;;
  esac
done

[[ ${#TARGETS[@]} -eq 0 ]] && TARGETS=("${ALL_TARGETS[@]}")

# ── 日志文件（参数解析完成后建立，--help 不产生日志）───────────────────────────
LOG_DIR="fuzz_logs"
mkdir -p "$LOG_DIR"
LOG_FILE="${LOG_DIR}/fuzz_$(date '+%Y%m%d_%H%M%S').log"
# terminal 保留颜色；日志文件剥离 ANSI 转义，输出纯文本
# tee/sed 必须忽略 SIGINT，否则 Ctrl+C 杀死 tee 后脚本写管道触发 SIGPIPE(141) 退出
exec > >(trap '' INT; exec tee >(trap '' INT; exec sed 's/\x1b\[[0-9;]*m//g' >> "$LOG_FILE")) 2>&1

# ── 颜色 ─────────────────────────────────────────────────────────────────────
if [[ -t 1 ]]; then
  RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
  CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'
else
  RED=''; GREEN=''; YELLOW=''; CYAN=''; BOLD=''; RESET=''
fi

# ── 辅助：打印带前缀的行 ───────────────────────────────────────────────────────
log()  { echo -e "${CYAN}[fuzz]${RESET} $*"; }
ok()   { echo -e "  ${GREEN}✓${RESET} $*"; }
warn() { echo -e "  ${YELLOW}⚠${RESET} $*"; }
err()  { echo -e "  ${RED}✗${RESET} $*"; }

# ── 编译检查（先跑一次，后续复用 build cache）────────────────────────
log "日志：${LOG_FILE}"
log "编译检查（${#FUZZ_PKGS[@]} 个包）…"
for pkg in "${FUZZ_PKGS[@]}"; do
  if ! go test -c -o /dev/null "./${pkg}" 2>&1; then
    err "${pkg}: 编译失败，终止。"
    echo "  → 详细错误见 ${LOG_FILE}"
    exit 1
  fi
done
ok "编译全部通过"
echo ""

# ── Ctrl+C 处理 ───────────────────────────────────────────────────────────────
SKIP_CURRENT=0   # 第一次 Ctrl+C：跳过当前目标
ABORT=0          # 第二次 Ctrl+C：退出整个脚本
FUZZ_PID=0

_on_sigint() {
  if [[ $SKIP_CURRENT -eq 1 ]]; then
    ABORT=1
    echo -e "\n${YELLOW}  已请求退出，正在停止…${RESET}"
  else
    SKIP_CURRENT=1
    echo -e "\n${YELLOW}  ↩ Ctrl+C — 中断当前目标，继续下一个（再按一次退出）${RESET}"
  fi
  [[ $FUZZ_PID -gt 0 ]] && kill "$FUZZ_PID" 2>/dev/null || true
}
trap '_on_sigint' INT

# ── 崩溃检测 ──────────────────────────────────────────────────────────────────
# 接受 "pkg/target" 格式；corpus 路径为 pkg/testdata/fuzz/target/

_check_crashes() {
  local pkg_target="$1"
  local pkg="${pkg_target%%/*}"
  local target="${pkg_target##*/}"
  local dir="${pkg}/testdata/fuzz/${target}"
  [[ -d "$dir" ]] || return 0

  # git 未跟踪的条目（?? 可能是目录级，需展开到具体文件）
  local raw
  raw=$(git status --porcelain -- "$dir" 2>/dev/null | awk '$1=="??" {print $2}')
  [[ -z "$raw" ]] && return 0

  local new_files=""
  while IFS= read -r entry; do
    if [[ -d "$entry" ]]; then
      # 整目录未跟踪：展开到文件列表
      while IFS= read -r f; do
        new_files+="${f}"$'\n'
      done < <(find "$entry" -type f | sort)
    else
      new_files+="${entry}"$'\n'
    fi
  done <<< "$raw"
  # 去除末尾空行
  new_files="${new_files%$'\n'}"
  [[ -z "$new_files" ]] && return 0

  echo -e "  ${RED}${BOLD}崩溃种子：${RESET}"
  while IFS= read -r f; do
    echo -e "    ${RED}${f}${RESET}"
  done <<< "$new_files"
  echo ""
  echo -e "  ${BOLD}复现（逐文件）：${RESET}"
  while IFS= read -r f; do
    local seed
    seed=$(basename "$f")
    echo -e "    go test -run '^${target}/${seed}$' -v ./${pkg}"
  done <<< "$new_files"
  echo ""
  echo -e "  ${BOLD}提交到仓库（下次 CI 自动回归）：${RESET}"
  while IFS= read -r f; do
    echo -e "    git add '${f}'"
  done <<< "$new_files"
  echo -e "    git commit -m 'fuzz: add crash seed for ${pkg}/${target}'"
  echo ""
}

# ── 主循环 ────────────────────────────────────────────────────────────────────
TOTAL=${#TARGETS[@]}
CRASHED=()
SKIPPED=0

log "目标 ${BOLD}${TOTAL}${RESET} 个，每个 ${BOLD}${FUZZ_TIME}${RESET}"
log "Ctrl+C 中断当前目标，继续下一个；再按退出"
echo ""

for i in "${!TARGETS[@]}"; do
  [[ $ABORT -eq 1 ]] && break
  SKIP_CURRENT=0

  pkg_target="${TARGETS[$i]}"
  pkg="${pkg_target%%/*}"
  target="${pkg_target##*/}"
  n=$((i + 1))

  echo -e "${CYAN}[${n}/${TOTAL}]${RESET} ${BOLD}${pkg_target}${RESET}  (${FUZZ_TIME})"

  # 后台运行，等待完成或被 Ctrl+C kill
  # -run 隔离：防止 go test 额外跑其他 Fuzz* 的种子模式
  go test -run="^${target}$" -fuzz="^${target}$" -fuzztime="${FUZZ_TIME}" "./${pkg}" &
  FUZZ_PID=$!
  wait $FUZZ_PID 2>/dev/null
  exit_code=$?
  # wait 被信号中断时返回 ≥128；子进程可能仍在退出中，再等一次以回收
  if [[ $exit_code -ge 128 ]] && [[ $FUZZ_PID -gt 0 ]]; then
    wait $FUZZ_PID 2>/dev/null || true
  fi
  FUZZ_PID=0

  [[ $ABORT -eq 1 ]] && { warn "已中断，退出。"; break; }

  if [[ $SKIP_CURRENT -eq 1 ]]; then
    warn "已跳过"
    ((SKIPPED++)) || true
  elif [[ $exit_code -ne 0 ]]; then
    err "退出码 ${exit_code}（可能发现崩溃）"
    CRASHED+=("$pkg_target")
    _check_crashes "$pkg_target"
  else
    ok "完成（无崩溃）"
  fi

  echo ""
done

# ── 汇总 ─────────────────────────────────────────────────────────────────────
echo -e "${BOLD}── 汇总 ──────────────────────────────────────────${RESET}"
ran=$((TOTAL - SKIPPED))
echo -e "  跑了 ${ran} / ${TOTAL} 个目标，每个 ${FUZZ_TIME}"

if [[ ${#CRASHED[@]} -gt 0 ]]; then
  echo -e "  ${RED}${BOLD}发现崩溃：${CRASHED[*]}${RESET}"
  echo -e "  ${YELLOW}修复后运行 ${BOLD}go test -run '^Fuzz' .${RESET}${YELLOW} 确认种子通过后提交。${RESET}"
  echo -e "  详细信息（含复现命令）：${BOLD}${LOG_FILE}${RESET}"
  exit 1
else
  echo -e "  ${GREEN}✓ 未发现崩溃${RESET}"
fi
echo -e "  完整日志：${LOG_FILE}"
