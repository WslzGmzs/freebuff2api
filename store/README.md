# CLIProxyAPI Plugins Store metadata

Spec: [CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store)

The official store **only** hosts a root `registry.json`. Plugin binaries and
`checksums.txt` are published from **this** repository’s GitHub Releases.

## Files in this directory

| File | Role |
|------|------|
| **`registry.json`** | Full store document shape (`schema_version` + `plugins[]`). Use for local validation / private store mirrors. |
| **`registry-entry.json`** | Single plugin object only. When opening a PR to the official store, **append this object** into their `plugins` array (do not replace their whole file with this object alone). |

## Official `registry.json` shape (required)

```json
{
  "schema_version": 1,
  "plugins": [
    {
      "id": "freebuff",
      "name": "Freebuff",
      "description": "...",
      "author": "WslzGmzs",
      "repository": "https://github.com/WslzGmzs/freebuff2api",
      "homepage": "https://github.com/WslzGmzs/freebuff2api",
      "license": "AGPL-3.0",
      "version": "0.1.0",
      "tags": ["Provider", "Freebuff", "Codebuff", "Management"]
    }
  ]
}
```

### Required fields (per plugin)

- `id` — plugin ID; must match library basename (`freebuff`)
- `name`
- `description`
- `author`
- `repository` — exactly `https://github.com/{owner}/{repo}` (no `.git`, no trailing slash)

### Optional

- `version` — display fallback only; **must not** start with `v` (use `0.1.0`, not `v0.1.0`)
- `logo`, `homepage`, `license`, `tags`

### Validation rules (store)

- `schema_version` must be `1`
- `id`: `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`
- `id` unique across the registry
- `repository` exact GitHub HTTPS form above

## Release assets (this repo, not in store)

Tag: `vX.Y.Z` (e.g. `v0.1.0`). Version in asset names = tag without `v`.

```text
freebuff_0.1.0_linux_amd64.zip
...
checksums.txt
```

Each zip: library **at zip root only** — `freebuff.so` / `freebuff.dylib` / `freebuff.dll`.

## Add to official store

1. Publish a valid `v*` GitHub Release (zips + `checksums.txt`).
2. Fork the store repo; edit **only** `registry.json`.
3. Append the object from `registry-entry.json` into `plugins` (keep existing plugins).
4. PR with: repo URL, latest tag, proof assets exist, short capability note.

Version bumps later: new release tag only; registry change optional.
