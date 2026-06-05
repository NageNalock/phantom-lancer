# Phantom Lancer

Personal all-in-one web terminal for managing self-hosted applications. The first implementation slice focuses on a Go backend, naked deployment, workspace registration, authentication, audit history, and Codex CLI task execution through the web UI.

## Run Locally

```bash
/opt/homebrew/bin/go run ./cmd/phantom-lancer
```

Open `http://127.0.0.1:8080`, create the owner account, then register a workspace under the allowed root.

By default, the service stores data in `.phantom-data` and seeds the allowed workspace root with the repo directory. For deployment, copy `configs/phantom.example.toml` to `configs/phantom.toml` or set `PL_CONFIG=/path/to/phantom.toml`.

The config file is intentionally small: listen address, data directory, optional database path, and first-run defaults. Runtime settings such as allowed roots, Codex binary, CODEX_HOME, and secure cookies are stored in SQLite and can be changed from the web Settings page without restarting.

## Scripts

```bash
cd web && npm install
scripts/build.sh
scripts/start.sh
scripts/manage.sh start
scripts/manage.sh status
scripts/manage.sh stop
scripts/manage.sh restart
```

Useful environment variables:

- `PL_CONFIG`: config file path, default `configs/phantom.toml` when present
- `PL_ADDR`: listen address, default `127.0.0.1:8080`
- `PL_DATA_DIR`: data directory, default `.phantom-data`
- `PL_ALLOWED_ROOTS`: initial comma-separated workspace roots when no DB setting exists
- `PL_BIN`: binary path, default `bin/phantom-lancer`
- `PL_CODEX_BINARY`: initial Codex binary when no DB setting exists
- `PL_CODEX_HOME`: initial Codex home directory when no DB setting exists
- `PL_SERVICE_LOG_FILE`: managed JSONL service log path, default `<data_dir>/logs/phantom-lancer.jsonl`
- `PL_LOG_FILE`: nohup stdout/stderr capture path for `manage.sh`
- `PL_LOG_MAX_SIZE_MB`, `PL_LOG_MAX_FILES`, `PL_LOG_MAX_AGE_DAYS`: service log rotation and cleanup limits
- `PL_PID_FILE`: pid file path for `manage.sh`
- `PL_SKIP_WEB_BUILD`: set to `1` to reuse existing `web/dist` during Go builds

## GitHub Actions

Pushes to `main` automatically build a Linux amd64 release archive. The same build can be started manually from GitHub Actions with the `Build` workflow. Pushing a `v*` tag, such as `v0.1.0`, also publishes the archive to GitHub Releases. See [docs/github-actions.md](docs/github-actions.md) for setup, release, and artifact deployment notes.

## Frontend

Frontend source lives in `web/src` and is built with Vite into `web/dist`, which the Go server embeds. Do not edit `web/dist` directly.

```bash
cd web
npm install
npm run dev
npm run build
```

The current source is a behavior-preserving TypeScript migration of the existing vanilla UI. Future frontend work should split stable domains into modules/components while keeping browser execution behind the Go `/api/*` boundary.

## Current Slice

- Go single-process backend.
- Embedded static web console.
- Owner bootstrap, login, session cookie, CSRF token.
- Workspace registration with path boundary checks.
- Codex CLI status detection.
- `codex exec --json` one-shot jobs with SSE events.
- Log center for managed service logs and runtime event logs, with bounded online tail.
- Basic audit trail.

Codex CLI installation and login are intentionally left to the server owner.

## License

Phantom Lancer is licensed under the GNU General Public License v3.0. See [LICENSE](LICENSE).
