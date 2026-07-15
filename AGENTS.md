# AGENTS.md — freebuff (CPA plugin)

## Purpose

Native **CLIProxyAPI** dual plugins from one tree:

| Binary | Identity (`-X plugin.Identity=…`) | auth.identifier | Role |
|--------|-------------------------------------|-----------------|------|
| `freebuff.*` | `freebuff` (default) | `freebuff` | models + executor + Freebuff OAuth |
| `codebuff.*` | `codebuff` | `codebuff` | Codebuff OAuth only |

CPA lists **one OAuth entry per identifier**; both libraries must be installed for both Freebuff OAuth and Codebuff OAuth on `/oauth`.

Login is only via CPA OAuth (`auth.login.*`). No management Token UI.

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
make plugins                 # freebuff.* + codebuff.*
make package-all VERSION=0.1.0
```

Link flags:

```text
-X github.com/WslzGmzs/freebuff2api/plugin.PluginVer=X.Y.Z
-X github.com/WslzGmzs/freebuff2api/plugin.Identity=freebuff|codebuff
```

## OAuth (`auth.login.*`)

| Library | auth.identifier | Host | UI label |
|---------|-----------------|------|----------|
| freebuff.* | freebuff | freebuff.com | Freebuff OAuth |
| codebuff.* | codebuff | codebuff.com | Codebuff OAuth |

Poll success always sets `AuthData.Provider=freebuff` so the freebuff executor handles chat.

## Credential rules

Parse top-level CPA fields into `AuthData`: `id`, `provider`, `prefix`, `label`, `proxy_url`, `priority`, `disabled`, `excluded_models`, `model_aliases`, `attributes`, `metadata`.  
Freebuff secrets stay in `StorageJSON` (`token`/`tokens`/…).  
Write-through to host: `Metadata`/`Attributes` get `disabled`, `excluded_models`, `model_aliases`, `auth_kind=oauth` (same pattern as workbuddy-cli-proxy).

### Model scope (OAuth-bound)

| API | Behavior |
|-----|----------|
| `ExecutorModelScope` | **`oauth`** (not both) |
| `model.static` | **empty** |
| `model.for_auth` | disabled / no storage → empty; else full list minus `excluded_models` |
| `execute` / `stream` | `disabled` → `auth_disabled`; excluded model → `model_excluded` |

## Architecture

1. No management resources / plugin-pages.  
2. Sessions model-bound; Gemini free → MiMo.  
3. Multi-token pool; strip non-function tools.  
4. Never set manual `Accept-Encoding: gzip` (breaks JSON with `\x1f`).  
5. Chat-completions executor only.  
6. Credential disable / model exclude / aliases follow workbuddy-cli-proxy.  
7. Upstream 4xx/429 must set error envelope `http_status` (+ RetryAfter when known) so CPA MarkResult cools/rotates auths; async stream connects before returning on execute_stream.

## Release

Tag `vX.Y.Z` → CI zips + `checksums.txt` for Plugins Store. See `store/README.md`.
