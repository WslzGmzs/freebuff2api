# freebuff

[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (CPA) 原生动态库插件：把 **Codebuff Freebuff** 免费模型接到 CPA。客户端走 OpenAI / Anthropic 协议即可调用。

实现参考 [workbuddy-cliproxy](https://github.com/lovingfish/workbuddy-cliproxy)、[CPA 插件开发文档](https://help.router-for.me/plugin/development.html) 与官方 [examples/plugin](https://github.com/router-for-me/CLIProxyAPI/tree/main/examples/plugin)。

## 能力

| CPA capability | 作用 |
|---|---|
| `model_provider` | Freebuff 模型（简写、Gemini→MiMo 别名） |
| `auth_provider` | `freebuff.json` 标准凭据 + **CPA `/oauth` 登录**（Freebuff OAuth / Codebuff OAuth） |
| `executor` | Session / 广告 / agent-run / 上游 SSE → chat-completions |

Anthropic 等协议由 **CPA 主机** 译成 chat-completions 后再进 executor。

## 要求

- CLIProxyAPI **v7.2.x**（CGO / 插件，`X-CPA-SUPPORT-PLUGIN: 1`）
- Go **1.26+** + 匹配架构的 `gcc`
- Freebuff / Codebuff 账号

## 构建

```bash
go test ./...
CGO_ENABLED=1 go build -buildmode=c-shared -o freebuff.dll .   # Windows: .dll / Linux: .so / macOS: .dylib
make plugin
make package VERSION=0.1.0
```

## 发布 / 插件商店

见 [CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store) 与 `store/`：

```bash
git tag v0.1.0 && git push origin v0.1.0
```

资产：`freebuff_<ver>_<goos>_<goarch>.zip` + `checksums.txt`（zip 根仅动态库）。

## 安装

1. 放入 `plugins/<goos>/<goarch>/freebuff.<ext>` 或 `plugins/freebuff.<ext>`
2. 配置：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    freebuff:
      enabled: true
      priority: 100
      # login_mode: freebuff   # 默认 Freebuff OAuth；codebuff = Codebuff OAuth
```

3. 重启 CPA，确认 `plugin loaded ... plugin_id=freebuff`。

## 登录（CPA `/oauth`）

在 CPA 管理界面 **OAuth / 添加凭据** 中选择本插件：

| 显示名 | 说明 |
|--------|------|
| **Freebuff OAuth** | 默认；`freebuff.com` CLI device-code |
| **Codebuff OAuth** | `login_mode: codebuff` 或启动 metadata `mode=codebuff`；`codebuff.com` |

流程：`auth.login.start` → 浏览器完成登录 → `auth.login.poll` → 校验 session API → 写入标准 `freebuff.json`。

也可手动粘贴 token（见下）。

## 凭据 `freebuff.json`（CPA 标准字段 + Freebuff 扩展）

完整示例：`auth/freebuff.example.json`。

**主机标准字段**（`pluginapi.AuthData` / CPA `Auth`）：

| 字段 | 说明 |
|------|------|
| `id` | 稳定凭据 ID |
| `provider` | 固定 `freebuff` |
| `label` | 展示名（如 `Freebuff OAuth`） |
| `prefix` | 模型前缀命名空间 |
| `proxy_url` | 该凭据上游代理（覆盖全局；空则直连） |
| `priority` | 调度优先级（写入 attributes/metadata） |
| `disabled` | 禁用 |
| `attributes` | 不可变路由属性 |
| `metadata` | 可变元数据 |

**Freebuff 扩展：**

| 字段 | 说明 |
|------|------|
| `token` / `tokens` | Bearer；逗号分隔或多账号数组 |
| `api_base_url` | 默认 `https://www.codebuff.com` |
| `ad_providers` | 默认 `gravity,zeroclick` |
| `login_mode` | `freebuff` \| `codebuff` |
| `timezone` / `locale` / `os` | 广告设备信息 |
| `debug` | 详细日志 |

`ToAuthData` 会把 `prefix` / `proxy_url` / `disabled` / `label` / `id` 填入 `AuthData`，供 CPA 调度与代理策略使用。

## 调用

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"你好"}],"stream":true}'
```

- 简写模型：`deepseek-v4-flash`
- `google/gemini-*` → 上游 `mimo/mimo-v2.5`（避免 409）

## 目录

```text
main.go                 # C ABI
plugin/dispatch.go      # CPA 方法：oauth / model / executor
freebuff/               # session、ads、runs、chat、login、标准凭据
auth/freebuff.example.json
store/                  # Plugins Store 元数据
.github/workflows/build.yml
```

## 已知修复

- **gzip JSON `\x1f`**：不再手写 `Accept-Encoding: gzip`，由 `net/http` 透明解压；并对残留 gzip 魔数做兜底解压。
