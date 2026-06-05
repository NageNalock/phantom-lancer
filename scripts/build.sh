#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${PL_BIN_DIR:-"$ROOT_DIR/bin"}"
OUT="${PL_OUT:-"$BIN_DIR/phantom-lancer"}"

find_npm() {
  if [[ -n "${PL_NPM_BIN:-}" ]]; then
    printf '%s\n' "$PL_NPM_BIN"
    return
  fi
  if command -v npm >/dev/null 2>&1; then
    command -v npm
    return
  fi
  if [[ -x /opt/homebrew/bin/npm ]]; then
    printf '%s\n' /opt/homebrew/bin/npm
    return
  fi
  if [[ -x /usr/local/bin/npm ]]; then
    printf '%s\n' /usr/local/bin/npm
    return
  fi
  printf 'npm binary not found. Install Node.js/npm or set PL_SKIP_WEB_BUILD=1 to reuse existing web/dist.\n' >&2
  exit 1
}

find_go() {
  if [[ -n "${PL_GO_BIN:-}" ]]; then
    printf '%s\n' "$PL_GO_BIN"
    return
  fi
  if command -v go >/dev/null 2>&1; then
    command -v go
    return
  fi
  if [[ -x /opt/homebrew/bin/go ]]; then
    printf '%s\n' /opt/homebrew/bin/go
    return
  fi
  if [[ -x /usr/local/bin/go ]]; then
    printf '%s\n' /usr/local/bin/go
    return
  fi
  printf 'go binary not found. Set PL_GO_BIN=/path/to/go.\n' >&2
  exit 1
}

GO_BIN="$(find_go)"

if [[ -n "${PL_GOROOT:-}" ]]; then
  export GOROOT="$PL_GOROOT"
elif [[ "$GO_BIN" == "/opt/homebrew/bin/go" && -d /opt/homebrew/opt/go/libexec ]]; then
  export GOROOT=/opt/homebrew/opt/go/libexec
elif [[ "$GO_BIN" == "/usr/local/bin/go" && -d /usr/local/opt/go/libexec ]]; then
  export GOROOT=/usr/local/opt/go/libexec
fi

mkdir -p "$BIN_DIR"
cd "$ROOT_DIR"

if [[ "${PL_SKIP_WEB_BUILD:-0}" != "1" && -f "$ROOT_DIR/web/package.json" ]]; then
  NPM_BIN="$(find_npm)"
  echo "Building frontend -> web/dist"
  PATH="$(dirname "$NPM_BIN"):$PATH" "$NPM_BIN" --prefix "$ROOT_DIR/web" run build
fi

echo "Building phantom-lancer -> $OUT"
"$GO_BIN" build -trimpath -o "$OUT" ./cmd/phantom-lancer
echo "Build complete: $OUT"
