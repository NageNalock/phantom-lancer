#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${PL_BIN:-"$ROOT_DIR/bin/phantom-lancer"}"
ADDR="${PL_ADDR:-127.0.0.1:8080}"
DATA_DIR="${PL_DATA_DIR:-"$ROOT_DIR/.phantom-data"}"
ALLOWED_ROOTS="${PL_ALLOWED_ROOTS:-"$ROOT_DIR"}"
CODEX_BINARY="${PL_CODEX_BINARY:-codex}"
COOKIE_SECURE="${PL_COOKIE_SECURE:-false}"
DEFAULT_CONFIG="$ROOT_DIR/configs/phantom.toml"
CONFIG_PATH="${PL_CONFIG:-"$DEFAULT_CONFIG"}"

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
    --codex "$CODEX_BINARY"
    --cookie-secure="$COOKIE_SECURE"
  )
  if [[ -n "${PL_DB_PATH:-}" ]]; then
    args+=(--db "$PL_DB_PATH")
  fi
  if [[ -n "${PL_CODEX_HOME:-}" ]]; then
    args+=(--codex-home "$PL_CODEX_HOME")
  fi
  if [[ -n "${PL_SERVICE_LOG_FILE:-}" ]]; then
    args+=(--log-file "$PL_SERVICE_LOG_FILE")
  fi
fi

exec "$BIN" "${args[@]}" "$@"
