#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
START_SCRIPT="${PL_START_SCRIPT:-"$ROOT_DIR/scripts/start.sh"}"
DATA_DIR="${PL_DATA_DIR:-"$ROOT_DIR/.phantom-data"}"
RUN_DIR="${PL_RUN_DIR:-"$DATA_DIR/run"}"
LOG_DIR="${PL_LOG_DIR:-"$DATA_DIR/logs"}"
PID_FILE="${PL_PID_FILE:-"$RUN_DIR/phantom-lancer.pid"}"
LOG_FILE="${PL_LOG_FILE:-"$LOG_DIR/phantom-lancer.nohup.log"}"
SERVICE_LOG_FILE="${PL_SERVICE_LOG_FILE:-"$LOG_DIR/phantom-lancer.jsonl"}"
STOP_TIMEOUT="${PL_STOP_TIMEOUT:-10}"

usage() {
  cat <<'USAGE'
Usage:
  scripts/manage.sh start [extra server args...]
  scripts/manage.sh stop
  scripts/manage.sh restart [extra server args...]
  scripts/manage.sh status
  scripts/manage.sh logs

Environment:
  PL_BIN             Binary path, default ./bin/phantom-lancer
  PL_CONFIG          Config file path, default ./configs/phantom.toml when present
  PL_ADDR            Listen address, default 127.0.0.1:8080
  PL_DATA_DIR        Data directory, default ./.phantom-data
  PL_ALLOWED_ROOTS   Initial workspace roots when no config file exists
  PL_DB_PATH         Optional SQLite DB path
  PL_COOKIE_SECURE   Initial true/false cookie default
  PL_SERVICE_LOG_FILE Structured service log, default ./.phantom-data/logs/phantom-lancer.jsonl
  PL_LOG_FILE        nohup stdout/stderr capture, default ./.phantom-data/logs/phantom-lancer.nohup.log
  PL_PID_FILE        PID file, default ./.phantom-data/run/phantom-lancer.pid
USAGE
}

read_pid() {
  if [[ -f "$PID_FILE" ]]; then
    cat "$PID_FILE"
  fi
}

is_running() {
  local pid="${1:-}"
  [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1
}

start_service() {
  mkdir -p "$RUN_DIR" "$LOG_DIR"

  local pid
  pid="$(read_pid || true)"
  if is_running "$pid"; then
    echo "phantom-lancer is already running (pid $pid)"
    return
  fi

  if [[ -n "$pid" ]]; then
    echo "Removing stale pid file: $PID_FILE"
    rm -f "$PID_FILE"
  fi

  echo "Starting phantom-lancer with nohup"
  echo "Service log: $SERVICE_LOG_FILE"
  echo "nohup capture: $LOG_FILE"
  nohup "$START_SCRIPT" "$@" >>"$LOG_FILE" 2>&1 &
  pid="$!"
  printf '%s\n' "$pid" >"$PID_FILE"
  sleep 1
  if ! is_running "$pid"; then
    echo "phantom-lancer failed to stay running. Last service log lines:"
    tail -n 20 "$SERVICE_LOG_FILE" || tail -n 20 "$LOG_FILE" || true
    rm -f "$PID_FILE"
    exit 1
  fi
  echo "Started phantom-lancer (pid $pid)"
}

stop_service() {
  local pid
  pid="$(read_pid || true)"
  if ! is_running "$pid"; then
    echo "phantom-lancer is not running"
    rm -f "$PID_FILE"
    return
  fi

  echo "Stopping phantom-lancer (pid $pid)"
  kill "$pid"

  local waited=0
  while is_running "$pid"; do
    if (( waited >= STOP_TIMEOUT )); then
      echo "Process did not stop after ${STOP_TIMEOUT}s; sending SIGKILL"
      kill -9 "$pid" || true
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done

  rm -f "$PID_FILE"
  echo "Stopped phantom-lancer"
}

status_service() {
  local pid
  pid="$(read_pid || true)"
  if is_running "$pid"; then
    echo "phantom-lancer is running (pid $pid)"
    echo "Service log: $SERVICE_LOG_FILE"
    echo "nohup capture: $LOG_FILE"
  else
    echo "phantom-lancer is not running"
    [[ -f "$PID_FILE" ]] && echo "Stale pid file: $PID_FILE"
  fi
  return 0
}

case "${1:-}" in
  start)
    shift
    start_service "$@"
    ;;
  stop)
    stop_service
    ;;
  restart)
    shift
    stop_service
    start_service "$@"
    ;;
  status)
    status_service
    ;;
  logs)
    mkdir -p "$LOG_DIR"
    touch "$SERVICE_LOG_FILE"
    tail -f "$SERVICE_LOG_FILE"
    ;;
  -h|--help|help|"")
    usage
    ;;
  *)
    echo "Unknown command: $1" >&2
    usage >&2
    exit 1
    ;;
esac
