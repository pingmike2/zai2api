#!/bin/bash
# GLM-ZAI-2API 容器入口
# 启动流程:
#   1. 首次快速采集一批 (TOKEN_BATCH, 默认150) 让服务尽快上线
#   2. 后台循环补采直到 token 池达到 TOKEN_TARGET (默认750), 达标停止
#   3. 达标后若池子被消耗到低水位 (< 50), 自动补一批
#   4. 采集失败自动回退直连重试

set -e

echo "[entrypoint] ==== zai2api 容器启动 ===="
mkdir -p /data
DB="${DB_PATH:-/data/tokens.sqlite}"
BATCH="${TOKEN_BATCH:-150}"          # 每批采集数
TARGET="${TOKEN_TARGET:-750}"        # 池子目标值
LOWWATER="${TOKEN_LOWWATER:-50}"     # 低水位(达标后被消耗到此时补采)

count_tokens() {
  if [ ! -f "$DB" ]; then echo 0; return; fi
  sqlite3 "$DB" "SELECT COUNT(*) FROM tokens;" 2>/dev/null || echo 0
}

# 代理支持: PROXY_URL 同时作用于采集 (ZAI_PROXY) 和服务 (HTTPS_PROXY)
if [ -n "$PROXY_URL" ]; then
  export ZAI_PROXY="$PROXY_URL"
  export HTTPS_PROXY="$PROXY_URL"
  export HTTP_PROXY="$PROXY_URL"
  echo "[entrypoint] 已启用代理: $PROXY_URL"
else
  echo "[entrypoint] 未配置 PROXY_URL, 直连"
fi

# 启动 Xvfb (chromedp 需要显示环境)
echo "[entrypoint] 启动 Xvfb..."
Xvfb :99 -screen 0 1280x900x24 &
XVFB_PID=$!
sleep 2
export DISPLAY=:99

# 采集一批 (代理失败自动回退直连)
collect_batch() {
  local label="$1"
  echo "[collect] ${label}: 采集 ${BATCH} 个 deviceToken..."
  if DISPLAY=:99 /app/token-collector --count "$BATCH" --out "$DB" --headless=true 2>&1; then
    echo "[collect] ✅ ${label} 采集完成: $(count_tokens) tokens"
    return 0
  fi
  if [ -n "$ZAI_PROXY" ]; then
    echo "[collect] ⚠️ 代理采集失败, 回退直连重试..."
    unset ZAI_PROXY HTTPS_PROXY HTTP_PROXY
    if DISPLAY=:99 /app/token-collector --count "$BATCH" --out "$DB" --headless=true 2>&1; then
      echo "[collect] ✅ 直连采集完成: $(count_tokens) tokens"
      return 0
    fi
  fi
  echo "[collect] ⚠️ 采集失败, 稍后重试"
  return 1
}

# 首次: 快速采一批让服务上线 (不阻塞)
TOKENS=$(count_tokens)
echo "[entrypoint] 当前 token 池: $TOKENS / 目标 $TARGET"
if [ "$TOKENS" -lt "$LOWWATER" ]; then
  echo "[entrypoint] 首次采集 (1 批, 不阻塞服务)..."
  collect_batch "首次" || echo "[entrypoint] 首次采集未成功, 服务先启动"
else
  echo "[entrypoint] token 池充足, 跳过首次采集"
fi

# 后台补采循环: 池子未达 TARGET 就持续补, 达标后低频检查
(
  while true; do
    T=$(count_tokens)
    if [ "$T" -lt "$TARGET" ]; then
      echo "[auto-collect] 池子 $T < 目标 $TARGET, 补采 1 批..."
      collect_batch "补采"
      sleep 60   # 未达标: 短间隔, 持续往目标爬
    elif [ "$T" -lt "$LOWWATER" ]; then
      echo "[auto-collect] 池子跌破低水位 ($T), 补采 1 批..."
      collect_batch "补采"
      sleep 60
    else
      sleep 600  # 达标且充足: 低频检查 (10分钟)
    fi
  done
) &
echo "[entrypoint] 后台补采循环已启动 (目标 $TARGET, 未达标每1分钟补批, 达标后10分钟检查)"

# 启动主服务
echo "[entrypoint] 启动 zai-server (port=${PORT:-8080})..."
echo "[entrypoint] ZAI_TOKEN: $([ -n "$ZAI_TOKEN" ] && echo '已配置(可解锁GLM-5.2)' || echo '未配置(仅glm-4.7)')"
exec /app/zai-server
