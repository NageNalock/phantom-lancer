#!/usr/bin/env bash
# 一键启动本地测试
# 用法: scripts/dev.sh
#   - 首次运行自动构建前端 + 后端
#   - 直接启动（绕过 supervisor），日志输出到终端
#   - Ctrl+C 停止
#
# 环境变量:
#   PL_SKIP_BUILD=1   跳过构建，直接用现有二进制
#   PL_SKIP_WEB_BUILD=1  跳过前端构建
#   PL_ADDR=host:port  监听地址，默认 127.0.0.1:8080
#   PL_DATA_DIR=...   数据目录，默认 ./.phantom-data
#   PL_OPEN_BROWSER=0  不自动打开浏览器

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADDR="${PL_ADDR:-127.0.0.1:8080}"
DATA_DIR="${PL_DATA_DIR:-"$ROOT_DIR/.phantom-data"}"
BIN_DIR="$ROOT_DIR/bin"
BIN="$BIN_DIR/phantom-lancer"
OPEN_BROWSER="${PL_OPEN_BROWSER:-1}"

cd "$ROOT_DIR"

# ---- 颜色 ----
if [[ -t 1 ]]; then
  C_RED=$'\033[31m'
  C_GREEN=$'\033[32m'
  C_YELLOW=$'\033[33m'
  C_BLUE=$'\033[34m'
  C_RESET=$'\033[0m'
else
  C_RED="" C_GREEN="" C_YELLOW="" C_BLUE="" C_RESET=""
fi

log()   { printf "${C_BLUE}[dev]${C_RESET} %s\n" "$*"; }
ok()    { printf "${C_GREEN}[dev]${C_RESET} %s\n" "$*"; }
warn()  { printf "${C_YELLOW}[dev]${C_RESET} %s\n" "$*" >&2; }
fail()  { printf "${C_RED}[dev]${C_RESET} %s\n" "$*" >&2; exit 1; }

# ---- 检查端口占用 ----
check_port() {
  local host port
  IFS=':' read -r host port <<<"$ADDR"
  if command -v lsof >/dev/null 2>&1; then
    if lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      local pid
      pid="$(lsof -t -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | head -1 || true)"
      warn "端口 $port 已被占用 (PID ${pid:-unknown})"
      read -r -p "是否杀掉占用进程并继续? [y/N] " ans
      case "$ans" in
        [yY]|[yY][eE][sS])
          if [[ -n "$pid" ]]; then
            kill "$pid" 2>/dev/null || true
            sleep 0.5
          fi
          ;;
        *)
          fail "请先手动释放端口 $port，或设置 PL_ADDR= 使用其他端口"
          ;;
      esac
    fi
  fi
}

# ---- 构建 ----
if [[ "${PL_SKIP_BUILD:-0}" != "1" ]]; then
  log "构建中..."
  PL_SKIP_WEB_BUILD="${PL_SKIP_WEB_BUILD:-0}" bash scripts/build.sh
  ok "构建完成"
else
  if [[ ! -x "$BIN" ]]; then
    fail "二进制不存在: $BIN  (去掉 PL_SKIP_BUILD=1 先构建)"
  fi
  log "跳过构建 (PL_SKIP_BUILD=1)"
fi

# ---- 准备数据目录 ----
mkdir -p "$DATA_DIR"

check_port

# ---- 启动 ----
log "启动 Phantom Lancer 本地测试服务"
log "  地址:    http://$ADDR"
log "  数据:    $DATA_DIR"
log "  二进制:  $BIN"
log ""
log "按 Ctrl+C 停止服务"
log "========================================"

# 打开浏览器
if [[ "$OPEN_BROWSER" == "1" ]]; then
  (
    sleep 1.5
    if command -v open >/dev/null 2>&1; then
      open "http://$ADDR"
    elif command -v xdg-open >/dev/null 2>&1; then
      xdg-open "http://$ADDR"
    fi
  ) &
fi

# 直接启动（无 supervisor），日志输出到终端
export PL_NO_SUPERVISOR=1
export PL_DATA_DIR
export PL_ADDR="$ADDR"

STOPPING=0

stop_server() {
  local sig="$1"
  STOPPING=1
  log ""
  log "收到停止信号(${sig})，正在关闭..."
  # 子进程会随 shell 退出被 SIGHUP，这里再确认一下
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  ok "已停止"
  case "$sig" in
    INT) exit 130 ;;
    TERM) exit 143 ;;
    *) exit 1 ;;
  esac
}

on_exit() {
  local status=$?
  if [[ "$STOPPING" == "1" ]]; then
    return "$status"
  fi
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [[ "$status" == "0" ]]; then
    ok "服务进程已退出"
  else
    warn "服务进程已退出 (exit=$status)，请查看上方输出或日志: $DATA_DIR/logs/phantom-lancer.jsonl"
  fi
  return "$status"
}
trap on_exit EXIT
trap 'stop_server INT' INT
trap 'stop_server TERM' TERM

"$BIN" \
  --addr "$ADDR" \
  --data-dir "$DATA_DIR" \
  --allowed-roots "$ROOT_DIR" \
  --cookie-secure=false \
  "$@" &
SERVER_PID=$!

wait "$SERVER_PID"
