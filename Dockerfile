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

# ============ Stage 2: 运行环境 (含 Chrome) ============
FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONUNBUFFERED=1

# 安装 Chrome + Xvfb + 依赖 (chromedp 需要真实 Chrome)
RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium \
    chromium-driver \
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
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/zai-server /app/zai-server
COPY --from=builder /out/token-collector /app/token-collector
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh /app/zai-server /app/token-collector

# 默认环境变量 (可被 NF 覆盖)
ENV PORT=8080
ENV HOST=0.0.0.0
ENV AUTH_TOKEN=d3vin
ENV ZAI_TOKEN=
ENV DB_PATH=/data/tokens.sqlite
ENV TOKEN_COUNT=150
ENV LOG_LEVEL=info

# 持久化目录 (token 池; NF 挂 volume 到 /data)
VOLUME ["/data"]

EXPOSE 8080

CMD ["/app/entrypoint.sh"]
