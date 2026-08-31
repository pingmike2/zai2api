#!/bin/bash
# GLM-ZAI-2API 容器入口
# 启动流程:
#   1. 若 token 池不足 → 容器内跑 token-collector 采集 (同 IP, 解决 F001)
#   2. 后台循环每 10 分钟检查 token 池, 不足 50 自动补采
#   3. 启动 Go 服务 (采集失败也启动, 不阻塞)

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

# 采集函数 (供前后台共用)
collect_once() {
  echo "[collect] 开始采集 ${TOKEN_COUNT:-300} 个 deviceToken..."
  if DISPLAY=:99 /app/token-collector --count "${TOKEN_COUNT:-300}" --out "$DB" --headless=true 2>&1; then
    echo "[collect] ✅ 采集完成: $(count_tokens) tokens"
    return 0
  else
    echo "[collect] ⚠️ 采集失败 (网络慢/验证码/被限), 转后台重试"
    return 1
  fi
}

# 首次采集: 只阻塞试 1 次 (~5 分钟), 失败立即启动服务, 后台继续补采
TOKENS=$(count_tokens)
echo "[entrypoint] 当前 token 池: $TOKENS"
if [ "$TOKENS" -lt 50 ]; then
  echo "[entrypoint] token 池不足, 首次采集 (1 次, 不阻塞服务)..."
  collect_once || echo "[entrypoint] 首次采集未成功, 服务先启动"
else
  echo "[entrypoint] token 池充足, 跳过首次采集"
fi

# 后台补采循环: 每 5 分钟检查, 不足 50 自动重采 (服务不停)
(
  while true; do
    sleep 300
    T=$(count_tokens)
    if [ "$T" -lt 50 ]; then
      echo "[auto-collect] token 池不足 ($T), 自动补采..."
      collect_once
    fi
  done
) &
echo "[entrypoint] 后台补采循环已启动 (每 5 分钟检查)"

# 启动主服务
echo "[entrypoint] 启动 zai-server (port=${PORT:-8080})..."
echo "[entrypoint] ZAI_TOKEN: $([ -n "$ZAI_TOKEN" ] && echo '已配置(可解锁GLM-5.2)' || echo '未配置(仅glm-4.7)')"
exec /app/zai-server
