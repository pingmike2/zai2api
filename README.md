# zai2api

Z.AI (chat.z.ai) **GLM 白嫖网关** — VPS 部署（支持 amd64 / arm64）。

基于 [D3-vin/GLM-ZAI-2API](https://github.com/D3-vin/GLM-ZAI-2API) (MIT, Go) 原版全功能移植，保留 dashboard / admin / tool-calling / 多模型支持。

## 为什么是 VPS 部署

Z.AI 的阿里云验证码 **deviceToken 与采集环境绑定**（同 IP 才有效）。VPS 上**同机采集 + 同机使用**，彻底解决跨环境 F001 问题。

## 部署状态

| 平台 | 状态 | 说明 |
|------|------|------|
| **VPS amd64 (dgnlinks)** | ✅ 可用 | systemd 原生部署，稳定运行 |
| **VPS arm64** | ✅ 可用 | Docker 部署 + SOCKS5 代理绕 WAF，稳定运行 |
| **容器部署 (Docker/Northflank)** | ⚠️ 理论支持 | 多架构镜像已推送 GHCR，但容器环境出口 IP 常被 WAF 拦截 |

## 功能

- **OpenAI 兼容 API**：`/v1/chat/completions`（流式/非流式）、`/v1/models`、`/v1/messages`
- **Tool calling**：函数调用（OpenCode / IDE 可用）
- **Dashboard**：`/`（Web UI）、`/admin/stats`、`/admin/health`、`/status`
- **模型**：
  - guest（无 ZAI_TOKEN）：`glm-4.7` ✅
  - 配置 `ZAI_TOKEN`（Z.AI 登录 JWT）：`GLM-5.2`、`GLM-5-Turbo`、`GLM-5v-Turbo`、`GLM-5.1` ✅
- **自动采集 token 池**：启动时采集 deviceToken（750 个/批），不足 50 自动补采

## 镜像

多架构镜像（amd64 + arm64）推送到 GHCR（workflow 自动构建）：

```
ghcr.io/pingmike2/zai2api:latest
```

## VPS 部署

### Docker 部署

```bash
docker run -d --name zai2api \
  -p 8080:8080 \
  -e AUTH_TOKEN=your-key \
  -e https_proxy=socks5://user:pass@host:port \
  -v zai2api-data:/data \
  --restart unless-stopped \
  ghcr.io/pingmike2/zai2api:latest
```

### 环境变量

| 变量 | 说明 | 默认 |
|------|------|------|
| `PORT` | 监听端口 | `8080` |
| `AUTH_TOKEN` | 客户端鉴权 key（**必设**，对应请求 Bearer） | `d3vin` |
| `ZAI_TOKEN` | Z.AI 登录 JWT（解锁 GLM-5.2，可选） | 空 |
| `TOKEN_BATCH` | 每批采集 token 数（服务先上线） | `150` |
| `TOKEN_TARGET` | token 池目标值（达标后停止补采） | `750` |
| `TOKEN_LOWWATER` | 低水位（消耗到此时补采） | `50` |
| `LOG_LEVEL` | 日志级别 | `info` |
| `http_proxy` | HTTP 代理（如 `socks5://user:pass@host:port`） | 空 |
| `https_proxy` | HTTPS 代理（同上，**必须设**，Z.AI 走 HTTPS） | 空 |

> **注意**：Z.AI 会对数据中心 IP 的 chat 请求返回 WAF 405。如果你的 VPS 是 DC IP，需要配置代理走住宅 IP。住宅 IP 的 VPS 不需要代理。
>
> **踩坑**：Go 的 `http.ProxyFromEnvironment` **只认小写** `http_proxy` / `https_proxy`，大写 `ALL_PROXY` / `HTTP_PROXY` **不会生效**。Docker `--env-file` 不会自动转小写，必须手动写小写变量名。

## 使用

```bash
# 模型列表
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer ***"

# chat (流式)
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ***" -H "Content-Type: application/json" \
  -d '{"model":"glm-4.7","messages":[{"role":"user","content":"hi"}]}'

# 状态
curl http://localhost:8080/status
curl http://localhost:8080/healthz
```

## 关于 GLM-5.2 无限用

配置 `ZAI_TOKEN`（chat.z.ai 登录后的 JWT）后 GLM-5.2 可用。是否"无限"取决于 Z.AI 账户额度——guest 无限制但只有 glm-4.7；带 token 的模型受 Z.AI 侧限流影响，实测为准。

## 免责

逆向自公开 MIT 项目，仅用于个人学习/测试。遵守 Z.AI 服务条款，控制请求频率。
