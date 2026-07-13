# freebuff

[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) (CPA) 原生动态库插件：把 **Codebuff Freebuff** 免费模型接到 CPA，任何支持 OpenAI / Anthropic 协议的客户端（Claude Code、Cursor、Cline、SDK…）都可直接调用。

实现参考 [workbuddy-cliproxy](https://github.com/lovingfish/workbuddy-cliproxy)、[CPA 插件开发文档](https://help.router-for.me/plugin/development.html) 与官方 [examples/plugin](https://github.com/router-for-me/CLIProxyAPI/tree/main/examples/plugin)。

## 能力

| CPA capability | 作用 |
|---|---|
| `model_provider` | 注册 Freebuff 模型（含简写、Gemini→MiMo 别名） |
| `auth_provider` | 解析 `freebuff.json`；**CLI 扫码登录**（原 `tool/get_token.py`） |
| `executor` | Session / 广告 / agent-run / 上游 SSE → chat-completions |
| `management_api` | 管理页 **Freebuff Token**（原 `tool/web` 登录 UI） |

Anthropic / 其他协议由 **CPA 主机** 先译成 chat-completions，再进入本插件 executor。

## 要求

- CLIProxyAPI **v7.2.x**（CGO / 插件支持，`X-CPA-SUPPORT-PLUGIN: 1`）
- 编译：Go **1.26+** + 与 CPA 同架构的 C 工具链（`gcc`）
- Freebuff 账号（浏览器登录拿 token）

## 构建

```bash
go test ./...

# Linux
CGO_ENABLED=1 go build -buildmode=c-shared -o freebuff.so .

# macOS
CGO_ENABLED=1 go build -buildmode=c-shared -o freebuff.dylib .

# Windows（PATH 上需有 MinGW gcc）
CGO_ENABLED=1 go build -buildmode=c-shared -o freebuff.dll .

# 或
make plugin    # dist/freebuff.<ext>
make package   # 额外打出 store 规范 zip + .sha256
```

产物架构必须与 CPA 进程一致（amd64 / arm64）。

## 发布 / 插件商店

本仓库按 [CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store) 规范发布：

| 项 | 约定 |
|----|------|
| 插件 ID | `freebuff` |
| Tag | `vX.Y.Z`（例：`v0.1.0`） |
| 资产名 | `freebuff_<version>_<goos>_<goarch>.zip` |
| 校验 | `checksums.txt`（sha256sum） |
| Zip 根目录 | 仅一个库：`freebuff.so` / `.dylib` / `.dll` |

```bash
git tag v0.1.0
git push origin v0.1.0
# GitHub Actions: test → 多平台 c-shared → zip → Release
```

工作流：`.github/workflows/build.yml`（linux/darwin/windows amd64+arm64、freebsd amd64）。  
商店登记条目草稿：`store/registry-entry.json`（向官方 store 提 PR 时用，版本升级一般只需打新 tag）。  
说明：`store/README.md`。

### 从 GitHub Release 安装

1. 在 CPA 插件商店或手动下载对应平台 zip，解压得到 `freebuff.<ext>`  
2. 放到 `plugins/<goos>/<goarch>/` 或 `plugins/`  
3. 配置见下；重启 CPA

## 安装

1. 把动态库放到 CPA 插件目录（推荐带平台路径）：

   ```text
   plugins/windows/amd64/freebuff.dll
   plugins/linux/amd64/freebuff.so
   plugins/darwin/arm64/freebuff.dylib
   # 或扁平：plugins/freebuff.<ext>
   ```

   插件 ID = 文件名去掉扩展名 → `freebuff`。

2. 在 CPA `config.yaml` 中启用：

   ```yaml
   plugins:
     enabled: true
     dir: "plugins"
     configs:
       freebuff:
         enabled: true
         priority: 100
         # login_mode: freebuff   # 或 codebuff
   ```

   完整片段见 `config.example.yaml`。

3. 重启 CPA，日志出现 `plugin loaded ... plugin_id=freebuff`。  
   可用 `GET /v0/management/plugins` 确认 `registered` / `effective_enabled`。

## 添加凭据（两种方式）

### A. CPA 面板扫码登录（推荐）

1. 在 CPA 管理界面为 **freebuff** 添加凭据 / 开始登录。  
2. 插件走 CLI device-code：`auth.login.start` → 打开浏览器 URL → `auth.login.poll` 直到成功。  
3. 成功后 token 写入 `freebuff.json`（含校验 `/api/v1/freebuff/session`）。

也可打开 **资源页**（**不需要** management key）：

```text
/v0/resource/plugins/freebuff/
```

页面支持 Freebuff / Codebuff 平台切换、扫码、验证 token、复制 `freebuff.json`。  
登录接口走同路径下的 resource API（同样免 management key）：

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/v0/resource/plugins/freebuff/api/start?mode=freebuff` | 开始 CLI 登录 |
| `GET` | `/v0/resource/plugins/freebuff/api/poll?state=...` | 轮询登录结果 |
| `GET` | `/v0/resource/plugins/freebuff/api/verify?token=...` | 校验 token |

（可选）`/v0/management/plugins/freebuff/...` 的 POST 路由仍保留，给已带 management key 的工具用。

### B. 手动写入 freebuff.json

模板：`auth/freebuff.example.json`

```json
{
  "token": "YOUR_FREEBUFF_BEARER_TOKEN",
  "tokens": ["token-a", "token-b"],
  "api_base_url": "https://www.codebuff.com",
  "ad_providers": ["gravity", "zeroclick"],
  "proxy_url": "",
  "timezone": "Asia/Shanghai",
  "locale": "zh-CN",
  "os": "windows",
  "debug": false
}
```

- `token`：单个或英文逗号分隔多账号  
- `tokens`：数组形式多账号；并发请求会租用空闲账号，避免 session 切模型互相覆盖  
- 默认**不**读系统 `HTTP_PROXY`；仅当 `proxy_url` 非空时走代理  

也可继续用公开页拿 token：<https://freebuff.071129.xyz/>，再粘贴进 json。

## 调用

CPA 默认端口常见为 `8317`，客户端 key 用 host `api-keys`。

```bash
curl http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek/deepseek-v4-flash",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

- 模型 id 支持简写：`deepseek-v4-flash`  
- `google/gemini-*` 免费标签是 **`mimo/mimo-v2.5` 别名**（同配额池，避免上游 409 `session_model_mismatch`）

| 协议 | Base URL（示例） |
|------|------------------|
| OpenAI | `http://<host>:8317/v1` |
| Anthropic | `http://<host>:8317`（`x-api-key`） |

## 目录结构

```text
main.go                 # C ABI 入口 + host 回调
plugin/                 # CPA 方法分发、登录 UI、management
freebuff/               # Freebuff 核心（session / ads / runs / chat / CLI login）
auth/freebuff.example.json
config.example.yaml
Makefile
go.mod
```

## Executor 流水线

1. 解析 `StorageJSON` → 多 token 账号池  
2. 解析模型（精确 / 后缀 / Gemini 别名）  
3. 租用空闲账号 + 绑定 session  
4. 广告链 → validate agents → agent-run（+ context-pruner）  
5. 构造上游 chat payload（`codebuff_metadata`，强制 stream）  
6. 流式/聚合 `/api/v1/chat/completions` SSE  
7. finalize run；释放账号  

## 开发

```bash
go test ./freebuff/ ./plugin/ -count=1
make plugin
```

相关文档：

- [CPA 插件开发](https://help.router-for.me/plugin/development.html)
- [官方插件示例](https://github.com/router-for-me/CLIProxyAPI/tree/main/examples/plugin)
