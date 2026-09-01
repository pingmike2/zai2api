# GLM-ZAI-2API (zai2api) — NF 容器版
# 容器内同 IP 采集 deviceToken + 运行原版 Go 服务 (保留全部功能: dashboard/admin/tools/GLM-5.2)

# ============ Stage 1: 编译 Go 二进制 ============
FROM golang:1.26-bookworm AS builder
WORKDIR /build

# 拷贝源码
COPY go.mod go.sum ./
COPY *.go ./
COPY token-collector/ ./token-collector/

# 编译主服务 + token-collector
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/zai-server .
RUN cd token-collector && CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/token-collector .

# 下载 gost (本地 socks5 中转: 解决 Chrome 不支持带认证 socks5)
# 把远程带认证 PROXY_URL 转成 127.0.0.1:1080 无认证端口
# 按构建架构下载 (amd64 / arm64 / armv7 等)
ARG TARGETARCH
RUN set -eux; \
    case "${TARGETARCH}" in \
      arm64) GOST_ARCH="arm64" ;; \
      arm)   GOST_ARCH="armv7" ;; \
      amd64) GOST_ARCH="amd64" ;; \
      *)     GOST_ARCH="${TARGETARCH}" ;; \
    esac; \
    curl -sSL -o /tmp/gost.tar.gz \
        https://github.com/go-gost/gost/releases/download/v3.3.0/gost_3.3.0_linux_${GOST_ARCH}.tar.gz \
    && tar xzf /tmp/gost.tar.gz -C /tmp \
    && mv /tmp/gost /out/gost \
    && chmod +x /out/gost

# ============ Stage 2: 运行环境 (含 Chrome + Node.js/Playwright) ============
# node:20-bookworm-slim 自带 Node.js + npm (Playwright 采集器需要)
FROM node:20-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONUNBUFFERED=1

# 安装 Chrome + Xvfb + 依赖 (Playwright 需要真实 Chrome)
RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium \
    xvfb \
    xauth \
    ca-certificates \
    fonts-liberation \
    libnss3 \
    libnspr4 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libxkbcommon0 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxrandr2 \
    libgbm1 \
    libasound2 \
    libpangocairo-1.0-0 \
    sqlite3 \
    build-essential \
    python3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/zai-server /app/zai-server
COPY --from=builder /out/gost /app/gost

# Playwright 采集器 (node 依赖)
COPY token-collector-js/ /app/token-collector-js/
RUN cd /app/token-collector-js && npm install --omit=dev 2>&1 | tail -3 \
    && npm rebuild better-sqlite3 --build-from-source 2>&1 | tail -2

COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh /app/zai-server /app/gost /app/token-collector-js/index.js

# 默认环境变量 (可被 NF 覆盖)
ENV PORT=8080
ENV HOST=0.0.0.0
ENV AUTH_TOKEN=d3vin
ENV ZAI_TOKEN=
ENV DB_PATH=/data/tokens.sqlite
ENV TOKEN_BATCH=150
ENV TOKEN_TARGET=750
ENV TOKEN_LOWWATER=50
ENV LOG_LEVEL=info

# 持久化目录 (token 池; NF 挂 volume 到 /data)
VOLUME ["/data"]

EXPOSE 8080

CMD ["/app/entrypoint.sh"]
