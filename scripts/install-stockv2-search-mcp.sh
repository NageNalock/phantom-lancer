#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA_DIR="${1:-${PL_DATA_DIR:-"$ROOT_DIR/.phantom-data"}}"
PACKAGE_VERSION="0.6.1"
PACKAGE_ROOT="$DATA_DIR/stockv2/mcp/duckduckgo"
VERSION_DIR="$PACKAGE_ROOT/$PACKAGE_VERSION"
CURRENT_LINK="$PACKAGE_ROOT/current"
SERVER_PYTHON="$VERSION_DIR/bin/python"

if ! command -v python3 >/dev/null 2>&1; then
  printf 'python3 is required to install the StockV2 search MCP.\n' >&2
  exit 1
fi

mkdir -p "$PACKAGE_ROOT"
if [[ -e "$VERSION_DIR" && ! -x "$SERVER_PYTHON" ]]; then
  printf 'Incomplete search MCP installation exists at %s; remove that exact directory and retry.\n' "$VERSION_DIR" >&2
  exit 1
fi

if [[ ! -x "$SERVER_PYTHON" ]]; then
  TEMP_DIR="$(mktemp -d "$PACKAGE_ROOT/.install-$PACKAGE_VERSION.XXXXXX")"
  cleanup() {
    rm -rf -- "$TEMP_DIR"
  }
  trap cleanup EXIT

  python3 -m venv "$TEMP_DIR"
  "$TEMP_DIR/bin/python" -m pip install \
    --disable-pip-version-check \
    --index-url https://pypi.org/simple \
    "duckduckgo-mcp-server[browser]==$PACKAGE_VERSION"
  if ! "$TEMP_DIR/bin/python" -c 'import importlib.util, sys; sys.exit(0 if importlib.util.find_spec("duckduckgo_mcp_server.server") else 1)'; then
    printf 'duckduckgo-mcp-server module was not installed.\n' >&2
    exit 1
  fi
  mv "$TEMP_DIR" "$VERSION_DIR"
  trap - EXIT
fi

TEMP_LINK="$PACKAGE_ROOT/.current.$$"
ln -s "$PACKAGE_VERSION" "$TEMP_LINK"
mv -Tf "$TEMP_LINK" "$CURRENT_LINK"
printf 'StockV2 search MCP ready: %s -m duckduckgo_mcp_server.server\n' "$CURRENT_LINK/bin/python"
