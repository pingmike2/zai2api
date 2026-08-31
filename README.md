# zai2api

Z.AI (chat.z.ai) **匿名 GLM-4.7** 白嫖网关 — Cloudflare Worker 版。

逆向自 [D3-vin/GLM-ZAI-2API](https://github.com/D3-vin/GLM-ZAI-2API) (MIT, Go)，纯 HTTP 复刻，无需账号、无需 API key。

## 原理

```
┌─ 一次性采集 (需 Chrome) ─────────────────────────┐
│ collect-tokens.mjs → 750 个 deviceToken → KV 池  │
└──────────────────────────────────────────────────┘
                    ↓
┌─ Worker 每次请求 ────────────────────────────────┐
│ GET /api/v1/auths/          → guest token (匿名)  │
│ InitCaptchaV3+VerifyCaptchaV3 → captcha_param     │
│ POST /api/v2/chat/completions → GLM-4.7 回复      │
└──────────────────────────────────────────────────┘
```

## 部署

```bash
npm i
npx wrangler login

# 1. 建 KV 命名空间
npx wrangler kv namespace create ZAI_TOKENS
# → 把输出的 id 填进 wrangler.toml

# 2. 采集 deviceToken (需有 Chrome, 跑一次出 750 个)
npx playwright install chromium
node collect-tokens.mjs --count 750 --out tokens.json

# 3. token 池灌进 KV
npx wrangler kv bulk put --binding=ZAI_TOKENS tokens.json

# 4. 部署
npx wrangler deploy
```

## 使用

```bash
# 模型列表
curl https://zai2api.<你的子域>.workers.dev/v1/models

# chat (非流式)
curl https://zai2api.<你的子域>.workers.dev/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-4.7","messages":[{"role":"user","content":"hi"}]}'

# chat (流式)
curl -N https://zai2api.<你的子域>.workers.dev/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"glm-4.7","messages":[{"role":"user","content":"hi"}],"stream":true}'
```

### Hermes 接入

```bash
hermes config set providers.zai base_url "https://zai2api.<你的子域>.workers.dev/v1"
hermes config set providers.zai model "glm-4.7"
hermes config set providers.zai key_env HERMES_CUSTOM_ZAI_API_KEY
# 不设客户端 key 时任意值即可 (如 "sk-none")
```

## 模型

| 模型 | guest 匿名 | 需 ZAI_TOKEN |
|------|:---:|:---:|
| `glm-4.7` | ✅ | — |
| `GLM-5-Turbo` | ❌ | ✅ |
| `GLM-5v-Turbo` | ❌ | ✅ |
| `GLM-5.1` | ❌ | ✅ |
| `glm-5.2` | ❌ | 有时 |

## 关键点 / 坑

- **deviceToken 一次性**：每次 chat 消耗一个 token（源码里 `tokenStore.remove`），750 个 ≈ 750 次请求。采完可再跑 collect-tokens.mjs 补充。
- **guest 会话纯 HTTP**：`GET /api/v1/auths/` 匿名返回 token+id，已验证可用。
- **captcha 纯 HTTP**：阿里云 InitCaptchaV3/VerifyCaptchaV3，HMAC-SHA1 签名，密钥对硬编码在逆向源码（非私密，公开）。
- **RC4-like 加密**：generateArg/encrypt 用自定义 64 位置换表 + 流密码，已复刻。
- **zlib 兼容**：Worker 用 CompressionStream('deflate') 生成 raw deflate，再手动包 zlib 头(0x78 0x9C)+adler32 尾，与 Go zlib.Writer 输出一致。
- **x-fe-Version**：硬编码 `prod-fe-1.1.92`（从 Z.AI 首页抓取，版本更新可能需调整）。
- **HMAC 签名**：`wKey = HMAC-SHA256(saltKey, bucket)`，`signature = HMAC-SHA256(wKey_hex, "requestId,..,timestamp,..,user_id,..|b64(prompt)|ts")`。

## 免责

逆向自公开 MIT 项目，仅用于个人学习/测试。遵守 Z.AI 服务条款，控制请求频率，勿商用。
