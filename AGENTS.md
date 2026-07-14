# AGENTS.md — freebuff (CPA plugin)

## Purpose

Native **CLIProxyAPI** plugin: Freebuff free models via `model_provider` + `auth_provider` (CPA **`/oauth`**: Freebuff OAuth / Codebuff OAuth) + `executor` (chat-completions).

**No** `management_api` / plugin-pages Token UI — login is only through CPA OAuth (`auth.login.*`).

## Layout

| Path | Role |
|---|---|
| `main.go` | C ABI + host stream callbacks |
| `plugin/dispatch.go` | Register, models, OAuth login, executor |
| `freebuff/client.go` | Codebuff HTTP (session/ads/runs/chat); gzip-safe JSON |
| `freebuff/session.go` | Session cache + multi-token pool |
| `freebuff/models.go` | Model registry, Gemini→MiMo |
| `freebuff/openai.go` | Payload / stream accumulate |
| `freebuff/login.go` | CLI device-code (Freebuff/Codebuff) + verify |
| `freebuff/config.go` | CPA-standard `AuthStorage` + `ToSettings` |
| `auth/freebuff.example.json` | Full credential example |
| `store/` | Plugins Store entry |
| `.github/workflows/build.yml` | Multi-platform release |

## Build

```bash
go test ./...
make plugin
make package VERSION=0.1.0
```

Version: `-X github.com/WslzGmzs/freebuff2api/plugin.PluginVer=X.Y.Z`

## OAuth (`auth.login.*`)

| Mode | Host | UI label |
|------|------|----------|
| `freebuff` (default) | freebuff.com | **Freebuff OAuth** |
| `codebuff` | codebuff.com | **Codebuff OAuth** |

Select via `plugins.configs.freebuff.login_mode` or start metadata `mode` / name containing `codebuff`.

## Credential rules

Parse top-level CPA fields into `AuthData`: `id`, `provider`, `prefix`, `label`, `proxy_url`, `priority`, `disabled`, `attributes`, `metadata`.  
Freebuff secrets stay in `StorageJSON` (`token`/`tokens`/…).  
Executor honors `disabled` and merges host `proxy_url` when storage empty.

## Architecture

1. No management resources / plugin-pages.  
2. Sessions model-bound; Gemini free → MiMo.  
3. Multi-token pool; strip non-function tools.  
4. Never set manual `Accept-Encoding: gzip` (breaks JSON with `\x1f`).  
5. Chat-completions executor only.

## Release

Tag `vX.Y.Z` → CI zips + `checksums.txt` for Plugins Store. See `store/README.md`.
