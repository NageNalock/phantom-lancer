#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
START_SCRIPT="${PL_START_SCRIPT:-"$ROOT_DIR/scripts/start.sh"}"
DATA_DIR="${PL_DATA_DIR:-"$ROOT_DIR/.phantom-data"}"
RUN_DIR="${PL_RUN_DIR:-"$DATA_DIR/run"}"
LOG_DIR="${PL_LOG_DIR:-"$DATA_DIR/logs"}"
# When started via start.sh the supervisor writes its own PID file with
# tmp+rename atomicity. manage.sh writes the same pid (from $! after nohup
# start) so both files are consistent.
PID_FILE="${PL_PID_FILE:-"$RUN_DIR/phantom-supervisor.pid"}"
CHILD_PID_FILE="${PL_CHILD_PID_FILE:-"$RUN_DIR/phantom-lancer.pid"}"
LOG_FILE="${PL_LOG_FILE:-"$LOG_DIR/phantom-lancer.nohup.log"}"
SERVICE_LOG_FILE="${PL_SERVICE_LOG_FILE:-"$LOG_DIR/phantom-lancer.jsonl"}"
SUPERVISOR_LOG_FILE="${PL_SUPERVISOR_LOG_FILE:-"$LOG_DIR/phantom-supervisor.jsonl"}"
STOP_TIMEOUT="${PL_STOP_TIMEOUT:-10}"

usage() {
  cat <<'USAGE'
Usage:
  scripts/manage.sh start [extra server args...]
  scripts/manage.sh stop
  scripts/manage.sh restart [extra server args...]
  scripts/manage.sh status
  scripts/manage.sh logs
  scripts/manage.sh supervisor-logs

Environment:
  PL_BIN                     Main binary path, default ./bin/phantom-lancer
  PL_SUPERVISOR_BIN          Supervisor binary path, default ./bin/phantom-supervisor
  PL_CONFIG                  Config file path, default ./configs/phantom.toml when present
  PL_ADDR                    Listen address, default 127.0.0.1:8080
  PL_DATA_DIR                Data directory, default ./.phantom-data
  PL_ALLOWED_ROOTS           Initial workspace roots when no config file exists
  PL_DB_PATH                 Optional SQLite DB path
  PL_COOKIE_SECURE           Initial true/false cookie default
  PL_SERVICE_LOG_FILE        Structured service log, default ./.phantom-data/logs/phantom-lancer.jsonl
  PL_SUPERVISOR_LOG_FILE     Supervisor structured log, default ./.phantom-data/logs/phantom-supervisor.jsonl
  PL_LOG_FILE                nohup stdout/stderr capture, default ./.phantom-data/logs/phantom-lancer.nohup.log
  PL_PID_FILE                Supervisor PID file, default ./.phantom-data/run/phantom-supervisor.pid
  PL_CHILD_PID_FILE          Child (phantom-lancer) PID file, default ./.phantom-data/run/phantom-lancer.pid
  PL_NO_SUPERVISOR=1         Fall back to direct launch (bypasses supervisor, no auto-restart)

Supervisor tuning (passed through to start.sh):
  PL_SUPERVISOR_RESTART_MIN_DELAY  e.g. "1s"
  PL_SUPERVISOR_RESTART_MAX_DELAY  e.g. "30s"
  PL_SUPERVISOR_STABLE_AFTER       e.g. "60s"
  PL_SUPERVISOR_STOP_TIMEOUT       e.g. "10s"
USAGE
}

read_pid() {
  local path="${1:-}"
  if [[ -f "$path" ]]; then
    tr -d '[:space:]' <"$path"
  fi
}

is_running() {
  local pid="${1:-}"
  [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1
}

start_service() {
  mkdir -p "$RUN_DIR" "$LOG_DIR"

  local sup_pid child_pid
  sup_pid="$(read_pid "$PID_FILE" || true)"
  if is_running "$sup_pid"; then
    echo "phantom-lancer supervisor is already running (pid $sup_pid)"
    child_pid="$(read_pid "$CHILD_PID_FILE" || true)"
    if is_running "$child_pid"; then
      echo "  child phantom-lancer running (pid $child_pid)"
    else
      echo "  child phantom-lancer not running (supervisor in backoff)"
    fi
    return
  fi

  if [[ -n "$sup_pid" ]]; then
    echo "Removing stale supervisor pid file: $PID_FILE"
    rm -f "$PID_FILE"
  fi
  if [[ -f "$CHILD_PID_FILE" ]]; then
    child_pid="$(read_pid "$CHILD_PID_FILE" || true)"
    if ! is_running "$child_pid"; then
      echo "Removing stale child pid file: $CHILD_PID_FILE"
      rm -f "$CHILD_PID_FILE"
    fi
  fi

  echo "Starting phantom-lancer (via supervisor) with nohup"
  echo "Service log:    $SERVICE_LOG_FILE"
  echo "Supervisor log: $SUPERVISOR_LOG_FILE"
  echo "nohup capture:  $LOG_FILE"
  nohup "$START_SCRIPT" "$@" >>"$LOG_FILE" 2>&1 &
  sup_pid="$!"
  printf '%s\n' "$sup_pid" >"$PID_FILE"
  sleep 1
  if ! is_running "$sup_pid"; then
    echo "phantom-lancer supervisor failed to stay running. Last log lines:"
    tail -n 20 "$SUPERVISOR_LOG_FILE" 2>/dev/null || tail -n 20 "$SERVICE_LOG_FILE" 2>/dev/null || tail -n 20 "$LOG_FILE" || true
    rm -f "$PID_FILE"
    exit 1
  fi
  echo "Started phantom-lancer supervisor (pid $sup_pid)"
  # Give the child a moment to come up before we announce its pid.
  for _ in 1 2 3 4 5; do
    child_pid="$(read_pid "$CHILD_PID_FILE" || true)"
    if [[ -n "$child_pid" ]]; then
      echo "  child phantom-lancer pid $child_pid"
      break
    fi
    sleep 0.5
  done
}

stop_service() {
  local sup_pid
  sup_pid="$(read_pid "$PID_FILE" || true)"
  if ! is_running "$sup_pid"; then
    echo "phantom-lancer supervisor is not running"
    rm -f "$PID_FILE"
    rm -f "$CHILD_PID_FILE" 2>/dev/null || true
    return
  fi

  echo "Stopping phantom-lancer supervisor (pid $sup_pid) — signal propagates to child"
  kill "$sup_pid"

  local waited=0
  while is_running "$sup_pid"; do
    if (( waited >= STOP_TIMEOUT )); then
      echo "Supervisor did not stop after ${STOP_TIMEOUT}s; sending SIGKILL"
      kill -9 "$sup_pid" || true
      # Also SIGKILL any orphaned child we can find via the PID file.
      local child_pid
      child_pid="$(read_pid "$CHILD_PID_FILE" || true)"
      if is_running "$child_pid"; then
        echo "Orphaned child still alive (pid $child_pid); sending SIGKILL"
        kill -9 "$child_pid" || true
      fi
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done

  rm -f "$PID_FILE" "$CHILD_PID_FILE" 2>/dev/null || true
  echo "Stopped phantom-lancer"
}

status_service() {
  local sup_pid child_pid
  sup_pid="$(read_pid "$PID_FILE" || true)"
  child_pid="$(read_pid "$CHILD_PID_FILE" || true)"
  local any=0
  if is_running "$sup_pid"; then
    echo "phantom-lancer supervisor is running (pid $sup_pid)"
    any=1
  else
    echo "phantom-lancer supervisor is NOT running"
    [[ -f "$PID_FILE" ]] && echo "  stale supervisor pid file: $PID_FILE"
  fi
  if is_running "$child_pid"; then
    echo "phantom-lancer child is running (pid $child_pid)"
    any=1
  elif [[ -n "$child_pid" ]]; then
    echo "phantom-lancer child is NOT running (stale pid $child_pid in $CHILD_PID_FILE)"
  else
    if is_running "$sup_pid"; then
      echo "phantom-lancer child not yet started (supervisor in backoff or startup)"
    else
      echo "phantom-lancer child is NOT running"
    fi
  fi
  if [[ "$any" -eq 1 ]]; then
    echo ""
    echo "Log files:"
    echo "  service:     $SERVICE_LOG_FILE"
    echo "  supervisor:  $SUPERVISOR_LOG_FILE"
    echo "  nohup:       $LOG_FILE"
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
  supervisor-logs)
    mkdir -p "$LOG_DIR"
    touch "$SUPERVISOR_LOG_FILE"
    tail -f "$SUPERVISOR_LOG_FILE"
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
