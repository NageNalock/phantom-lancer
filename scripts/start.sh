#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${PL_BIN:-"$ROOT_DIR/bin/phantom-lancer"}"
SUPERVISOR_BIN="${PL_SUPERVISOR_BIN:-"$ROOT_DIR/bin/phantom-supervisor"}"
ADDR="${PL_ADDR:-127.0.0.1:8080}"
DATA_DIR="${PL_DATA_DIR:-"$ROOT_DIR/.phantom-data"}"
ALLOWED_ROOTS="${PL_ALLOWED_ROOTS:-"$ROOT_DIR"}"
COOKIE_SECURE="${PL_COOKIE_SECURE:-false}"
DEFAULT_CONFIG="$ROOT_DIR/configs/phantom.toml"
CONFIG_PATH="${PL_CONFIG:-"$DEFAULT_CONFIG"}"

RUN_DIR="${PL_RUN_DIR:-"$DATA_DIR/run"}"
LOG_DIR="${PL_LOG_DIR:-"$DATA_DIR/logs"}"
SUPERVISOR_PID_FILE="${PL_SUPERVISOR_PID_FILE:-"$RUN_DIR/phantom-supervisor.pid"}"
CHILD_PID_FILE="${PL_CHILD_PID_FILE:-"$RUN_DIR/phantom-lancer.pid"}"
LOCK_FILE="${PL_LOCK_FILE:-"$RUN_DIR/phantom-supervisor.lock"}"
HANDOFF_FILE="${PL_HANDOFF_FILE:-"$RUN_DIR/update-handoff.json"}"
SUPERVISOR_LOG_FILE="${PL_SUPERVISOR_LOG_FILE:-"$LOG_DIR/phantom-supervisor.jsonl"}"

if [[ ! -x "$BIN" ]]; then
  printf 'Binary not found or not executable: %s\n' "$BIN" >&2
  printf 'Run scripts/build.sh first, or set PL_BIN=/path/to/phantom-lancer.\n' >&2
  exit 1
fi

args=()
if [[ -f "$CONFIG_PATH" || -n "${PL_CONFIG:-}" ]]; then
  if [[ ! -f "$CONFIG_PATH" ]]; then
    printf 'Config file not found: %s\n' "$CONFIG_PATH" >&2
    exit 1
  fi
  args+=(--config "$CONFIG_PATH")
else
  mkdir -p "$DATA_DIR"
  args=(
    --addr "$ADDR"
    --data-dir "$DATA_DIR"
    --allowed-roots "$ALLOWED_ROOTS"
    --cookie-secure="$COOKIE_SECURE"
  )
  if [[ -n "${PL_DB_PATH:-}" ]]; then
    args+=(--db "$PL_DB_PATH")
  fi
  if [[ -n "${PL_SERVICE_LOG_FILE:-}" ]]; then
    args+=(--log-file "$PL_SERVICE_LOG_FILE")
  fi
fi

# Escape hatch: if supervisor is disabled via env var, fall back to direct launch.
if [[ "${PL_NO_SUPERVISOR:-0}" == "1" ]]; then
  export PL_DATA_DIR
  exec "$BIN" "${args[@]}" "$@"
fi

# If the supervisor binary is missing or not executable, warn and fall back.
if [[ ! -x "$SUPERVISOR_BIN" ]]; then
  printf 'warning: phantom-supervisor binary not found at %s; falling back to direct launch (auto-restart disabled)\n' "$SUPERVISOR_BIN" >&2
  export PL_DATA_DIR
  exec "$BIN" "${args[@]}" "$@"
fi

mkdir -p "$RUN_DIR" "$LOG_DIR"
export PL_DATA_DIR
export PL_RUN_DIR="$RUN_DIR"
export PL_LOG_DIR="$LOG_DIR"

sup_args=()
sup_args+=(--pid-file "$SUPERVISOR_PID_FILE")
sup_args+=(--child-pid-file "$CHILD_PID_FILE")
sup_args+=(--lock-file "$LOCK_FILE")
sup_args+=(--handoff-file "$HANDOFF_FILE")
sup_args+=(--log-file "$SUPERVISOR_LOG_FILE")
if [[ -n "${PL_SUPERVISOR_RESTART_MIN_DELAY:-}" ]]; then
  sup_args+=(--restart-min-delay "$PL_SUPERVISOR_RESTART_MIN_DELAY")
fi
if [[ -n "${PL_SUPERVISOR_RESTART_MAX_DELAY:-}" ]]; then
  sup_args+=(--restart-max-delay "$PL_SUPERVISOR_RESTART_MAX_DELAY")
fi
if [[ -n "${PL_SUPERVISOR_STABLE_AFTER:-}" ]]; then
  sup_args+=(--stable-after "$PL_SUPERVISOR_STABLE_AFTER")
fi
if [[ -n "${PL_SUPERVISOR_STOP_TIMEOUT:-}" ]]; then
  sup_args+=(--stop-timeout "$PL_SUPERVISOR_STOP_TIMEOUT")
fi

# exec replaces the shell with the supervisor process, so $! (from the caller
# like manage.sh) ends up being exactly the supervisor PID.
exec "$SUPERVISOR_BIN" "${sup_args[@]}" -- "$BIN" "${args[@]}" "$@"
