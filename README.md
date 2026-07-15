# freebuff

[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (CPA) 原生动态库插件：把 **Codebuff Freebuff** 免费模型接到 CPA。客户端走 OpenAI / Anthropic 协议即可调用。

实现参考 [workbuddy-cliproxy](https://github.com/lovingfish/workbuddy-cliproxy)、[CPA 插件开发文档](https://help.router-for.me/plugin/development.html) 与官方 [examples/plugin](https://github.com/router-for-me/CLIProxyAPI/tree/main/examples/plugin)。

## 能力

| 动态库 | `auth.identifier` | `/oauth` 显示 | 其它 |
|--------|-------------------|---------------|------|
| **`freebuff.*`** | `freebuff` | **Freebuff OAuth** | 模型 + chat executor |
| **`codebuff.*`** | `codebuff` | **Codebuff OAuth** | 仅登录；凭据仍走 freebuff executor |

CPA 每个插件库只能注册 **一个** auth provider，因此 Codebuff OAuth 必须作为第二个库安装（同源代码，`-X plugin.Identity=codebuff`）。

Anthropic 等协议由 **CPA 主机** 译成 chat-completions 后再进 executor。

## 要求

- CLIProxyAPI **v7.2.x**（CGO / 插件，`X-CPA-SUPPORT-PLUGIN: 1`）
- Go **1.26+** + 匹配架构的 `gcc`
- Freebuff / Codebuff 账号

## 构建

```bash
go test ./...

# 两个库（推荐）：Freebuff OAuth + Codebuff OAuth
make plugins          # dist/freebuff.<ext> + dist/codebuff.<ext>
make package-all VERSION=0.1.0

# 仅 Freebuff
make freebuff-plugin
```

## 发布 / 插件商店

见 [CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store) 与 `store/`：

```bash
git tag v0.1.0 && git push origin v0.1.0
```

资产：`freebuff_<ver>_<goos>_<goarch>.zip` + `checksums.txt`（zip 根仅动态库）。

## 安装

1. 将 **两个** 库放入 CPA 插件目录（平台子目录或扁平均可）：

   ```text
   plugins/windows/amd64/freebuff.dll
   plugins/windows/amd64/codebuff.dll
   # Linux: .so   macOS: .dylib
   ```

2. 配置（见 `config.example.yaml`）：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    freebuff: { enabled: true, priority: 100 }
    codebuff: { enabled: true, priority: 99 }
```

3. 重启 CPA，日志中应有 `plugin_id=freebuff` 与 `plugin_id=codebuff`。

## 登录（CPA `/oauth`）

CPA 根据各库的 `auth.identifier` 暴露 OAuth 入口（对应  
`GET /v0/management/freebuff-auth-url` / `codebuff-auth-url`）：

| `/oauth` 项 | 插件库 | 上游登录 |
|-------------|--------|----------|
| **Freebuff OAuth** | `freebuff.*` | freebuff.com CLI device-code |
| **Codebuff OAuth** | `codebuff.*` | codebuff.com CLI device-code |

流程：`auth.login.start` → 浏览器登录 → `auth.login.poll` → 校验 session → 写入凭据。  
登录成功后 `AuthData.Provider` 为 **`freebuff`**（执行路由），但 **磁盘凭据文件与 ID 按来源分开**：

| OAuth | 默认文件名 | ID 前缀 | `login_mode` |
|-------|------------|---------|--------------|
| Freebuff OAuth | `freebuff.json` / `freebuff-<hash>.json` | `freebuff-` | `freebuff` |
| Codebuff OAuth | `codebuff.json` / `codebuff-<hash>.json` | `codebuff-` | `codebuff` |

同一 token 走两种 OAuth 也会得到不同 id/文件，不会互相覆盖。  
聊天仍由 freebuff executor 执行。

也可手动粘贴 token（见 `auth/freebuff.example.json`、`auth/codebuff.example.json`）。

## 凭据 `freebuff.json` / `codebuff.json`（CPA 标准字段 + Freebuff 扩展）

完整示例：`auth/freebuff.example.json`、`auth/codebuff.example.json`。

**主机标准字段**（`pluginapi.AuthData` / CPA `Auth`，对齐 workbuddy-cli-proxy）：

| 字段 | 说明 |
|------|------|
| `id` | 稳定凭据 ID（`freebuff-` / `codebuff-` 前缀） |
| `provider` | 固定 `freebuff`（执行路由） |
| `label` | 展示名（如 `Freebuff OAuth`） |
| `prefix` | 模型前缀命名空间 |
| `proxy_url` | 该凭据上游代理（覆盖全局；空则直连） |
| `priority` | 调度优先级（写入 attributes/metadata） |
| `disabled` | 禁用后：不出现模型、execute 返回 `auth_disabled` |
| `excluded_models` | 该凭据隐藏的模型 id（也支持 `excluded-models`） |
| `model_aliases` | CPA 模型别名 `[{name,alias,force-mapping}]`（也支持 `model-aliases`） |
| `attributes` | 含 `auth_kind=oauth`、`excluded_models`、`model_aliases` 等 |
| `metadata` | 含 `disabled` / 排除 / 别名（供 CPA 主机合并） |

模型范围：`ExecutorModelScope=oauth`，`model.static` 为空；仅 **启用中的凭据** 经 `model.for_auth` 挂模型。全部禁用后 `/v1/models` 不再出现 freebuff 模型。

也可在 CPA `config.yaml` 使用全局：

```yaml
oauth-model-alias:
  freebuff:
    - name: deepseek/deepseek-v4-flash
      alias: ds-flash
oauth-excluded-models:
  freebuff:
    - mimo/mimo-v2.5
```

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
