# CLIProxyAPI Plugins Store metadata

Spec: [CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store)

The official store **only** hosts a root `registry.json`. Plugin binaries and
`checksums.txt` are published from **this** repository’s GitHub Releases.

## Files in this directory

| File | Role |
|------|------|
| **`registry.json`** | Full store document with **both** `freebuff` and `codebuff` entries. |
| **`registry-entry.json`** | `freebuff` only — append to official store PR. |
| **`registry-entry-codebuff.json`** | `codebuff` only — append as a **second** store entry (same repo). |

CPA store install looks up **latest release** of `repository` for asset:

```text
<id>_<version>_<goos>_<goarch>.zip
```

So `id: freebuff` installs `freebuff_…zip` only.  
To install Codebuff OAuth from the store you need a **separate** registry entry `id: codebuff` **and** release assets named `codebuff_…zip`.

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

## Dual libraries (Freebuff + Codebuff OAuth)

CPA exposes **one** OAuth entry per `auth.identifier`. This repo builds two libraries:

| id | Asset zip | Library in zip | `/oauth` |
|----|-----------|----------------|----------|
| `freebuff` | `freebuff_<ver>_<os>_<arch>.zip` | `freebuff.<ext>` | Freebuff OAuth + chat |
| `codebuff` | `codebuff_<ver>_<os>_<arch>.zip` | `codebuff.<ext>` | Codebuff OAuth |

## Release assets (this repo, not in store)

Tag: `vX.Y.Z` (e.g. `v0.1.0`). Version in asset names = tag without `v`.

```text
freebuff_0.1.0_linux_amd64.zip
codebuff_0.1.0_linux_amd64.zip
...
checksums.txt
```

Each zip: library **at zip root only** (`freebuff.so|dll|dylib` or `codebuff.*`).

## Add to official store

1. Publish a valid `v*` GitHub Release (both zips + `checksums.txt`).
2. Fork the store repo; edit **only** `registry.json`.
3. Append entries for `freebuff` (and optionally `codebuff`) from `registry-entry.json` style objects.
4. PR with: repo URL, latest tag, proof assets exist, short capability note.

Version bumps later: new release tag only; registry change optional.
