# GitHub Actions Build

This repository includes a GitHub Actions workflow that builds and packages the embedded Go server for Linux amd64.

## Trigger

The workflow lives at `.github/workflows/build.yml` and runs in three ways:

- Automatically after every push to `main`. Main branch pushes publish a GitHub Release by incrementing the latest `vX.Y.Z` tag by one patch version.
- Manually from GitHub: `Actions` -> `Build` -> `Run workflow`.
- Automatically after pushing a version tag that matches `v*`, such as `v0.1.0`. Tag builds also publish or update the matching GitHub Release.

Manual runs expose two inputs:

- `run_tests`: runs `npm run check` and `go test ./...` before packaging. Keep this enabled for normal builds.
- `artifact_name`: overrides the uploaded artifact base name. The default is `phantom-lancer-linux-amd64`.

## Build Steps

The workflow:

1. Checks out the repository.
2. Installs Go from `go.mod`.
3. Installs Node.js 22 and frontend dependencies with `npm ci`.
4. Type-checks the frontend when tests are enabled.
5. Builds `web/dist`, which is embedded by `web/static.go`.
6. Runs Go tests when tests are enabled.
7. Builds `bin/phantom-lancer` with CGO enabled and musl static linking for portable Linux self-updates.
8. Creates a tarball containing:
   - `bin/phantom-lancer`
   - `scripts/start.sh`
   - `scripts/manage.sh`
   - `configs/phantom.example.toml`
   - `README.md`
   - `LICENSE`
9. Uploads the tarball and a `.sha256` checksum as a workflow artifact.
10. For main pushes and `v*` tags, creates or updates the matching GitHub Release and uploads the same files as release assets.

## GitHub Setup

1. Push this repository to GitHub with the workflow file included.
2. Open the GitHub repository settings.
3. Go to `Actions` -> `General`.
4. Make sure actions are enabled for the repository.
5. Keep the workflow file's permissions as defined. The build job uses read-only repository access; the release job requests `contents: write` only when creating tags and publishing a GitHub Release.

No repository secret is required for the current build workflow.

If the release job fails with `Resource not accessible by integration`, open `Settings` -> `Actions` -> `General` and set `Workflow permissions` to allow read and write permissions for the repository.

## Publish a Release

Release publishing is automatic for `main`. On each push to `main`, the workflow finds the latest semantic release tag matching `vX.Y.Z`, increments the patch number, creates that tag at the pushed commit, and publishes a GitHub Release. For example, if the latest tag is `v0.1.4`, the next main push publishes `v0.1.5`.

Manual tag publishing is still supported. Use semantic version style tags with a `v` prefix:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The tag push triggers the same Linux amd64 build and then creates or updates a GitHub Release named `Phantom Lancer v0.1.0`. The Release contains:

- `phantom-lancer-linux-amd64.tar.gz`
- `phantom-lancer-linux-amd64.tar.gz.sha256`

Re-running the same tag workflow updates the existing Release assets with `--clobber`.

## Download and Run an Artifact

After a workflow run finishes:

1. Open the run detail page under the `Actions` tab.
2. Download the `phantom-lancer-linux-amd64` artifact.
3. Extract the downloaded GitHub artifact zip.
4. Copy `phantom-lancer-linux-amd64.tar.gz` and `phantom-lancer-linux-amd64.tar.gz.sha256` to the Linux server.
5. Verify the checksum if needed:

```bash
sha256sum -c phantom-lancer-linux-amd64.tar.gz.sha256
```

6. Extract and configure:

```bash
tar -xzf phantom-lancer-linux-amd64.tar.gz
cd phantom-lancer
cp configs/phantom.example.toml configs/phantom.toml
```

7. Edit `configs/phantom.toml` for the target server, then start the service:

```bash
scripts/start.sh
```

For managed background execution:

```bash
scripts/manage.sh start
scripts/manage.sh status
scripts/manage.sh logs
```

## Platform Notes

The current workflow intentionally builds only `linux-amd64`. The backend uses `github.com/mattn/go-sqlite3`, so CGO must be enabled. Release binaries are linked statically with musl to avoid depending on the GitHub runner's glibc version; this is important for self-updates on older Linux distributions.

For an ARM server, prefer adding a native ARM64 runner or a separate CGO-aware build job instead of assuming simple Go cross-compilation will work.
