# AGENTS.md — freebuff (CPA plugin)

## Purpose

Native **CLIProxyAPI (CPA)** dynamic library that wraps **Codebuff Freebuff** free models: `model_provider` + `auth_provider` (CLI device-code login) + `executor` (chat-completions) + `management_api` (token helper UI).

## Layout

| Path | Role |
|---|---|
| `main.go` | C ABI (`cliproxy_plugin_init` / call / free / shutdown), host stream callbacks |
| `plugin/dispatch.go` | Method router, registration, auth parse/login, executor |
| `plugin/management.go` | Management routes + resource HTML handler |
| `plugin/tokenui.go` | Embedded Freebuff Token helper page |
| `freebuff/client.go` | Codebuff HTTP: session, ads, agent-runs, chat SSE |
| `freebuff/session.go` | Per-model session cache + multi-token `AccountPool` |
| `freebuff/models.go` | Hardcoded models, Gemini→MiMo aliases, agent map |
| `freebuff/openai.go` | Message normalize, upstream payload, stream accumulator |
| `freebuff/login.go` | CLI device-code login + token verify (ex-`tool/get_token`) |
| `freebuff/config.go` | `AuthStorage` / `Settings` (`freebuff.json` shape) |
| `auth/freebuff.example.json` | Credential template |
| `config.example.yaml` | CPA `plugins.configs.freebuff` snippet |
| `store/registry-entry.json` | Official Plugins Store registry draft |
| `store/README.md` | Store / release asset rules |
| `.github/workflows/build.yml` | Multi-platform build + GitHub Release |
| `.github/scripts/package-release.go` | Zip root library + sha256 line |
| `README.md` | User-facing build / install / usage |

## Build & test

```bash
go test ./... -count=1
make plugin              # dist/freebuff.<ext>
make package VERSION=0.1.0   # store zip + .sha256
```

Requires Go **1.26+**, CGO, `gcc` matching host arch. Plugin ID from filename: `freebuff`.

Version injection (CI / Makefile):

```text
-X github.com/WslzGmzs/freebuff2api/plugin.PluginVer=X.Y.Z
```

SDK: `github.com/router-for-me/CLIProxyAPI/v7` (`pluginabi`, `pluginapi`).

## Release (Plugins Store)

Spec: https://github.com/router-for-me/CLIProxyAPI-Plugins-Store

1. Tag `vX.Y.Z` (no other tag shape).  
2. CI produces `freebuff_X.Y.Z_<goos>_<goarch>.zip` + merged `checksums.txt`.  
3. Each zip: library **at zip root only** (`freebuff.so|dylib|dll`).  
4. First listing: PR into store `registry.json` using `store/registry-entry.json`.  
5. Later versions: new release only; registry `version` is optional display fallback.

## CPA wiring

| Method | Behavior |
|---|---|
| `plugin.register` / `reconfigure` | Metadata + capabilities |
| `model.static` / `model.for_auth` | Model list (+ bare suffixes) |
| `auth.parse` | Accept `freebuff.json` with `token` / `tokens` |
| `auth.login.start` / `poll` | Freebuff/Codebuff CLI code flow; poll until token; verify session API |
| `auth.refresh` | Echo auth (bearer tokens have no refresh endpoint) |
| `executor.execute` / `execute_stream` | Full Freebuff pipeline → chat-completions |
| `management.register` / `handle` | Token UI + `/login/start|poll|verify` |

Install: `plugins/<goos>/<goarch>/freebuff.<ext>`, `plugins.enabled: true`, credential in CPA auth dir.

## Architecture rules

1. **Keep layers separate** — `freebuff/` = upstream I/O & domain; `plugin/` = CPA envelopes only; `main.go` = C ABI.  
2. **Sessions are model-bound** — chat model must match Freebuff session or upstream **409**. Set `UpstreamModelID` / `SessionModelID` / `AgentID` together in `models.go`.  
3. **Gemini free → MiMo** — `google/gemini-*` routes to `mimo/mimo-v2.5` + `base2-free-mimo`. Do not send Gemini ids upstream.  
4. **Multi-token pool** — comma-separated / `tokens[]`; lease idle accounts under concurrency.  
5. **Tools** — strip non-`function` tools before upstream.  
6. **Proxy** — never trust env proxy unless credential `proxy_url` is set.  
7. **Executor formats** — chat-completions only; host handles Anthropic/etc.  
8. **Login** — live in `freebuff/login.go`; do not reintroduce a standalone Python `tool/` server.

## Credential shape (`freebuff.json`)

```json
{
  "token": "bearer-or-a,b,c",
  "tokens": ["a", "b"],
  "api_base_url": "https://www.codebuff.com",
  "ad_providers": ["gravity", "zeroclick"],
  "proxy_url": "",
  "timezone": "Asia/Shanghai",
  "locale": "zh-CN",
  "os": "windows",
  "debug": false,
  "label": "optional"
}
```

Never commit real tokens. Ignore `auth/freebuff.json` in git.

## Conventions

- Go style: typed errors (`freebuff.Error`), context timeouts on HTTP, no panic in plugin handlers.  
- New models: edit `freebuff/models.go` + tests; keep display aliases in `_DISPLAY` map.  
- After logic changes: `go test ./...`; with gcc, rebuild `c-shared` and reload CPA plugin.

## Read first

- `README.md` — install & login  
- `freebuff/models.go` — routing comments  
- `freebuff/login.go` — CLI auth contract  
- `plugin/dispatch.go` — CPA surface  
