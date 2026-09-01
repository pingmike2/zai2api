# zai2api

Z.AI (chat.z.ai) **GLM 白嫖网关** — 容器版 (Northflank/Docker)。

基于 [D3-vin/GLM-ZAI-2API](https://github.com/D3-vin/GLM-ZAI-2API) (MIT, Go) 原版全功能移植，保留 dashboard / admin / tool-calling / 多模型支持。

## 为什么是容器版

Z.AI 的阿里云验证码 **deviceToken 与采集环境绑定**（同 IP 才有效）。容器内**同机采集 + 同机使用**，彻底解决跨环境 F001 问题。CF Worker 无法满足此要求，故放弃。

## 部署状态

| 平台 | 状态 | 说明 |
|------|------|------|
| **VPS (dgnlinks x86_64)** | ✅ 可用 | systemd 原生部署，稳定运行 |
| **Northflank 容器 arm64** | ❌ 失败 | 容器出口 IP 为数据中心 IP，被 z.ai 设备指纹风控拦截，deviceToken 采集失败 |
| **arm64 本地 / 自行编译** | 🔧 可行 | 需本地 `go build` 或 `docker build`，代码开源，NF 镜像仅供参考 |

## 功能

- **OpenAI 兼容 API**：`/v1/chat/completions`（流式/非流式）、`/v1/models`、`/v1/messages`
- **Tool calling**：函数调用（OpenCode / IDE 可用）
- **Dashboard**：`/`（Web UI）、`/admin/stats`、`/admin/health`、`/status`
- **模型**：
  - guest（无 ZAI_TOKEN）：`glm-4.7` ✅
  - 配置 `ZAI_TOKEN`（Z.AI 登录 JWT）：`GLM-5.2`、`GLM-5-Turbo`、`GLM-5v-Turbo`、`GLM-5.1` ✅
- **自动采集 token 池**：启动时容器内采集 deviceToken（750 个/批），不足 50 自动补采

## 镜像

构建推送到 GHCR（workflow 自动）：

```
ghcr.io/pingmike2/zai2api:latest
```

## Northflank 部署

1. **新建 Service** → 从 GitHub 仓库构建（zai2api 已公开）或拉取 GHCR 镜像
2. **环境变量**：

| 变量 | 说明 | 默认 |
|------|------|------|
| `PORT` | 监听端口 | `8080` |
| `AUTH_TOKEN` | 客户端鉴权 key（**必设**，对应请求 Bearer） | `d3vin` |
| `ZAI_TOKEN` | Z.AI 登录 JWT（解锁 GLM-5.2，可选） | 空 |
| `TOKEN_BATCH` | 每批采集 token 数（服务先上线） | `150` |
| `TOKEN_TARGET` | token 池目标值（达标后停止补采） | `750` |
| `TOKEN_LOWWATER` | 低水位（消耗到此时补采） | `50` |
| `LOG_LEVEL` | 日志级别 | `info` |

3. **Volume**：挂载 `/data` 持久化 token 池（否则重启后重新采集）
4. **端口**：暴露 `8080`
5. **Health check**：`/healthz`

## 本地运行

```bash
docker build -t zai2api .
docker run -d -p 8080:8080 \
  -e AUTH_TOKEN=your-key \
  -v zai2api-data:/data \
  zai2api
```

## 使用

```bash
# 模型列表
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer your-key"

# chat (流式)
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-key" -H "Content-Type: application/json" \
  -d '{"model":"glm-4.7","messages":[{"role":"user","content":"hi"}]}'

# 状态
curl http://localhost:8080/status
curl http://localhost:8080/healthz
```

## 关于 GLM-5.2 无限用

配置 `ZAI_TOKEN`（chat.z.ai 登录后的 JWT）后 GLM-5.2 可用。是否"无限"取决于 Z.AI 账户额度——guest 无限制但只有 glm-4.7；带 token 的模型受 Z.AI 侧限流影响，实测为准。

## 免责

逆向自公开 MIT 项目，仅用于个人学习/测试。遵守 Z.AI 服务条款，控制请求频率。
