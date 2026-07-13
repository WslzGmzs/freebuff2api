# CLIProxyAPI Plugins Store entry

This directory holds the metadata used to list **freebuff** in the official
[CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store).

The store **only** hosts `registry.json`. Binaries, checksums, and release notes
live in **this** repository's GitHub Releases.

## Registry entry

See [`registry-entry.json`](./registry-entry.json). Required fields:

| Field | Value |
|-------|--------|
| `id` | `freebuff` (must match library filename without extension) |
| `name` | Freebuff |
| `description` | short capability summary |
| `author` | WslzGmzs |
| `repository` | `https://github.com/WslzGmzs/freebuff2api` (exact form) |

Optional: `version` (display fallback), `logo`, `homepage`, `license`, `tags`.

`version` must **not** start with `v`. Release tags **must** be `vX.Y.Z`.

## Release assets (this repo)

Produced by `.github/workflows/build.yml` on tag `v*`:

```text
freebuff_<version>_darwin_amd64.zip
freebuff_<version>_darwin_arm64.zip
freebuff_<version>_linux_amd64.zip
freebuff_<version>_linux_arm64.zip
freebuff_<version>_windows_amd64.zip
freebuff_<version>_windows_arm64.zip
freebuff_<version>_freebsd_amd64.zip
checksums.txt
```

Each zip contains **only** the dynamic library at the zip root:

- Darwin: `freebuff.dylib`
- Linux / FreeBSD: `freebuff.so`
- Windows: `freebuff.dll`

`checksums.txt` uses sha256sum lines:

```text
<sha256>  freebuff_0.1.0_linux_amd64.zip
```

## Publish a version

```bash
# ensure plugin.PluginVer / docs match
git tag v0.1.0
git push origin v0.1.0
# CI builds all platforms and creates/updates the GitHub Release
```

## Add to official store

1. Ship at least one valid `v*` release with zips + `checksums.txt`.
2. Fork [CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store).
3. Append the object from `registry-entry.json` to the `plugins` array in `registry.json`.
4. Open a PR that **only** changes `registry.json` (unless docs need updates), and include:
   - repository URL
   - latest release tag (`v0.1.0`)
   - evidence that zip assets + `checksums.txt` exist
   - short capability description

Updates: publish a new `v*` release only; **no** registry change required for version bumps.
