#!/bin/bash
# GLM-ZAI-2API 容器入口
# 启动流程:
#   1. 若 token 池不足 → 容器内跑 token-collector 采集 (同 IP, 解决 F001)
#   2. 启动 Go 服务

set -e

echo "[entrypoint] ==== zai2api 容器启动 ===="
mkdir -p /data
DB="${DB_PATH:-/data/tokens.sqlite}"

count_tokens() {
  if [ ! -f "$DB" ]; then echo 0; return; fi
  sqlite3 "$DB" "SELECT COUNT(*) FROM tokens;" 2>/dev/null || echo 0
}

# 启动 Xvfb (chromedp 需要显示环境)
echo "[entrypoint] 启动 Xvfb..."
Xvfb :99 -screen 0 1280x900x24 &
XVFB_PID=$!
sleep 2
export DISPLAY=:99

# token 池检查: 不足 50 个则重新采集
TOKENS=$(count_tokens)
echo "[entrypoint] 当前 token 池: $TOKENS"
if [ "$TOKENS" -lt 50 ]; then
  echo "[entrypoint] token 池不足, 容器内采集 ${TOKEN_COUNT:-750} 个 (同 IP)..."
  DISPLAY=:99 /app/token-collector --count "${TOKEN_COUNT:-750}" --out "$DB" --headless=true
  echo "[entrypoint] 采集完成: $(count_tokens) tokens"
else
  echo "[entrypoint] token 池充足, 跳过采集"
fi

# 启动主服务
echo "[entrypoint] 启动 zai-server (port=${PORT:-8080})..."
echo "[entrypoint] ZAI_TOKEN: $([ -n "$ZAI_TOKEN" ] && echo '已配置(可解锁GLM-5.2)' || echo '未配置(仅glm-4.7)')"
exec /app/zai-server
