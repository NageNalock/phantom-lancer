# Phantom Lancer

Personal all-in-one web terminal for managing self-hosted applications. The first implementation slice focuses on a Go backend, naked deployment, workspace registration, authentication, audit history, and the Codex Gateway (OpenAI-compatible API) managed through the web UI.

## Run Locally

```bash
/opt/homebrew/bin/go run ./cmd/phantom-lancer
```

Open `http://127.0.0.1:8080`, create the owner account, then register a workspace under the allowed root.

By default, the service stores data in `.phantom-data` and seeds the allowed workspace root with the repo directory. For deployment, copy `configs/phantom.example.toml` to `configs/phantom.toml` or set `PL_CONFIG=/path/to/phantom.toml`.

The config file is intentionally small: listen address, data directory, optional database path, and first-run defaults. Runtime settings such as allowed roots and secure cookies are stored in SQLite and can be changed from the web Settings page without restarting.

## Run From a Release Package

The published release package is currently built for Linux amd64. On a new server, download the release archive and checksum from GitHub Releases:

```bash
VERSION=v0.1.0
curl -L -o phantom-lancer-linux-amd64.tar.gz \
  "https://github.com/NageNalock/phantom-lancer/releases/download/${VERSION}/phantom-lancer-linux-amd64.tar.gz"
curl -L -o phantom-lancer-linux-amd64.tar.gz.sha256 \
  "https://github.com/NageNalock/phantom-lancer/releases/download/${VERSION}/phantom-lancer-linux-amd64.tar.gz.sha256"
sha256sum -c phantom-lancer-linux-amd64.tar.gz.sha256
```

Extract it and create the first config file:

```bash
tar -xzf phantom-lancer-linux-amd64.tar.gz
cd phantom-lancer
cp configs/phantom.example.toml configs/phantom.toml
```

Edit `configs/phantom.toml` for the new machine before starting:

- `server.addr`: the listen address, for example `127.0.0.1:8080` for local-only access.
- `storage.data_dir`: where Phantom Lancer stores SQLite data, logs, update backups, and runtime assets.
- `bootstrap.allowed_roots`: first-run workspace roots that the web console may register.
- `updates.restart_mode`: keep `exit` when the service is supervised by systemd with restart enabled.

Start it in the foreground:

```bash
scripts/start.sh
```

Or run it as a managed background process with the bundled script:

```bash
scripts/manage.sh start
scripts/manage.sh status
scripts/manage.sh logs
```

Open the configured address in a browser, create the owner account, then register the first workspace under an allowed root.

For long-running deployment, run Phantom Lancer under a supervisor such as systemd and configure automatic restart. This is recommended for the web self-update flow because updates replace the binary and then request a short service restart.

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
- `PL_SERVICE_LOG_FILE`: managed JSONL service log path, default `<data_dir>/logs/phantom-lancer.jsonl`
- `PL_LOG_FILE`: nohup stdout/stderr capture path for `manage.sh`
- `PL_LOG_MAX_SIZE_MB`, `PL_LOG_MAX_FILES`, `PL_LOG_MAX_AGE_DAYS`: service log rotation and cleanup limits
- `PL_PID_FILE`: pid file path for `manage.sh`
- `PL_SKIP_WEB_BUILD`: set to `1` to reuse existing `web/dist` during Go builds
- `PHANTOM_MASTER_KEY`: master key for wrapped credentials stored in the DB,
  encoded as unpadded base64-raw-URL and decoding to **at least 32 bytes**
  (the implementation accepts `>=keywrap.MinMasterKeyBytes` so operators
  may supply larger keys if required by policy).
  When set, this value takes priority over the per-install key generated
  into `settings.system.crypto_master_key_v1` and is **never** written
  back to the SQLite database. If the env is set and no DB key has been
  generated yet, the service skips generating one, so a "pure env"
  deployment has no copy of the master key anywhere in the SQLite file.
  Use this in deployments where the threat model calls for
  key↔ciphertext separation — it prevents an attacker who copies only
  the SQLite file (e.g. an unauthorised backup restore, a leaked DB
  snapshot) from decrypting stored credential tokens. If the env var
  is unset the service falls back to the key stored in the `settings`
  table, which still guards against accidental plaintext exposure in
  `SELECT` output but offers no defence against full-DB exfiltration.

## GitHub Actions

Pushes to `main` automatically build and publish a Linux amd64 GitHub Release. The workflow increments the latest `vX.Y.Z` tag by one patch version, then builds the binary with that version. The same build can be started manually from GitHub Actions, and manually pushed `v*` tags are still supported. See [docs/github-actions.md](docs/github-actions.md) for setup, release, and artifact deployment notes.

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
- Codex Gateway: OpenAI-compatible endpoint, upstream account/credential management, public API keys, model catalog, and request logs.
- Log center for managed service logs and runtime event logs, with bounded online tail.
- Basic audit trail.

## License

Phantom Lancer is licensed under the GNU General Public License v3.0. See [LICENSE](LICENSE).
