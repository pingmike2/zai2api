/**
 * zai2api — Z.AI (chat.z.ai) 匿名 GLM-4.7 白嫖网关 (Cloudflare Worker)
 *
 * 逆向自 D3-vin/GLM-ZAI-2API (MIT, Go) — 纯 HTTP 复刻：
 *   1. GET  /api/v1/auths/            → guest token (匿名, 免注册)
 *   2. InitCaptchaV3 + VerifyCaptchaV3 → captcha_verify_param (阿里云, HMAC-SHA1)
 *   3. POST /api/v2/chat/completions  → GLM-4.7 (HMAC-SHA256 签名)
 *
 * 依赖: KV "ZAI_TOKENS" — deviceToken 池 (由 collect-tokens.mjs 采集, 一次性 750 个)
 *
 * 环境变量:
 *   ZAI_AUTH_KEY  客户端访问鉴权 (请求头 Authorization: Bearer xxx)
 *   ZAI_KV        可选, 指定 KV 命名空间名 (默认 DUCKAI-style)
 */

// ============ 常量 (来自逆向) ============
const BASE = "https://chat.z.ai";
const CAPTCHA_INIT = "https://no8xfe.captcha-open-southeast.aliyuncs.com/";
const CAPTCHA_VERIFY = "https://no8xfe-verify.captcha-open-southeast.aliyuncs.com/";
const CAPTCHA_AK = "LTAI5tSEBwYMwVKAQGpxmvTd";
const CAPTCHA_SK = "YSKfst7GaVkXwZYvVihJsKF9r89koz";
const CAPTCHA_SCENE = "didk33e0";
const SALT_KEY = "key-@@@@)))()((9))-xxxx&&&%%%%%";
const ARG_CONST = "4xrihv8zb8tf1mfj";
const ENCRYPT_KEY = "3e627e1b4c63f913";
const ARG_PERM = [32,50,10,51,6,44,37,16,46,11,62,19,43,25,23,30,60,33,53,34,7,26,12,48,5,2,20,4,61,13,47,49,18,29,27,22,1,17,39,56,41,38,55,31,15,58,52,40,8,57,45,35,59,36,42,54,63,3,24,28,14,9,0,21];

const FE_VERSION = "prod-fe-1.1.92";
const DEFAULT_MODEL = "glm-4.7";
const UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36";

// ============ 工具 ============
const b64encode = (s) => btoa(String.fromCharCode(...new TextEncoder().encode(s)));
const b64decode = (s) => new TextDecoder().decode(Uint8Array.from(atob(s), c => c.charCodeAt(0)));
const hexLower = "0123456789abcdef";

function uuidV4() {
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0F) | 0x40;
  b[8] = (b[8] & 0x3F) | 0x80;
  let s = "";
  for (let i = 0; i < 16; i++) {
    if (i === 4 || i === 6 || i === 8 || i === 10) s += "-";
    s += hexLower[b[i] >> 4] + hexLower[b[i] & 0xF];
  }
  return s;
}

function urlEncode(s, safe = "") {
  const safeSet = new Set(("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~" + safe).split(""));
  let out = "";
  for (const c of s) out += safeSet.has(c) ? c : urlEnc(c);
  return out;
}

// 单个字符 → 大写百分号编码
function urlEnc(c) {
  const h = c.charCodeAt(0).toString(16).toUpperCase();
  return "%" + (h.length === 1 ? "0" + h : h);
}

// HMAC-SHA256 hex (Web Crypto)
async function hmacSha256Hex(key, data) {
  const k = await crypto.subtle.importKey("raw", new TextEncoder().encode(key), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = await crypto.subtle.sign("HMAC", k, new TextEncoder().encode(data));
  return [...new Uint8Array(sig)].map(b => b.toString(16).padStart(2, "0")).join("");
}

// HMAC-SHA1 base64 (阿里云签名)
async function hmacSha1B64(key, data) {
  const k = await crypto.subtle.importKey("raw", new TextEncoder().encode(key), { name: "HMAC", hash: "SHA-1" }, false, ["sign"]);
  const sig = await crypto.subtle.sign("HMAC", k, new TextEncoder().encode(data));
  return btoa(String.fromCharCode(...new Uint8Array(sig)));
}

// zlib 压缩 — CompressionStream('deflate') 已输出完整 zlib 格式 (0x78 0x9C 头 + adler32 尾)
// 实测与 Go zlib.Writer 输出逐字节一致 (Node 22)
async function zlibCompress(data) {
  const cs = new CompressionStream("deflate");
  const writer = cs.writable.getWriter();
  writer.write(data);
  writer.close();
  return new Uint8Array(await new Response(cs.readable).arrayBuffer());
}

// RC4-like 加密 (generateArg / encrypt 共用)
function rc4Like(input, keyStr, perm) {
  const r = [...perm];
  const rlen = 64;
  let i = 0, j = 0;
  while (i < rlen) {
    j = (((i + j + r[i] + r[j]) >> 1) + keyStr.charCodeAt(i % keyStr.length)) & (rlen - 1);
    if (i !== j) { const t = r[i]; r[i] = r[j]; r[j] = t; }
    i++;
  }
  const out = new Uint8Array(input.length);
  let e = 0, a = 0;
  for (let idx = 0; idx < input.length; idx++) {
    a = ((e ^ a) + (r[e] ^ r[a])) & (rlen - 1);
    if (e !== a) { const t = r[e]; r[e] = r[a]; r[a] = t; }
    let m = input[idx] + e + r[e] - a - r[a];
    m = m ^ (r[e] + r[a]);
    m = m ^ r[(r[e] + r[a]) & (rlen - 1)];
    out[idx] = m & 255;
    e = (e + 1) & (rlen - 1);
  }
  return out;
}

// aliHash (16 字节状态自定义哈希 → 32 hex)
function aliHash(inputStr, saltStr) {
  const e = new Array(16);
  for (let i = 0; i < 16; i++) e[i] = (i << 4) + (i % 16);
  const f = 16;
  let i = 0, j = 0;
  while (i < f) {
    j = (((i + j + e[i] + e[j]) >> 1) + saltStr.charCodeAt(i % saltStr.length)) & (f - 1);
    const t = e[i]; e[i] = e[j]; e[j] = t;
    i++;
  }
  let p = 0, q = 0;
  for (let idx = 0; idx < inputStr.length; idx++) {
    q = ((p ^ q) + (e[p] ^ e[q])) & (f - 1);
    const t = e[p]; e[p] = e[q]; e[q] = t;
    let C = (inputStr.charCodeAt(idx) + p + q) ^ e[p] ^ e[q];
    C = C & 255;
    e[p] = C;
    p = (p + 1) & (f - 1);
  }
  for (let step = 0; step < 2 * f; step++) {
    const pos = step % f;
    if (pos !== 0) e[pos] ^= e[pos - 1];
    else e[0] ^= e[f - 1];
  }
  let out = "";
  for (const b of e) out += hexLower[(b >> 4) & 0xF] + hexLower[b & 0xF];
  return out;
}

// ============ 阿里云验证码 ============
async function aliyunSignature(params, secKey) {
  const keys = Object.keys(params).sort();
  // ⚠️ 用 urlEncode (整串编码), 不能用 urlEnc (单字符) — Go 源码是 urlEncode(canonical)
  const canonical = keys.map(k => urlEncode(k) + "=" + urlEncode(params[k])).join("&");
  const stringToSign = "POST&" + urlEncode("/") + "&" + urlEncode(canonical);
  return hmacSha1B64(secKey + "&", stringToSign);
}

async function aliyunRequest(url, params, extraHeaders = {}) {
  params.Signature = await aliyunSignature(params, CAPTCHA_SK);
  const body = Object.keys(params).sort().map(k => urlEncode(k) + "=" + urlEncode(params[k])).join("&");
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8", "User-Agent": UA, ...extraHeaders },
    body,
  });
  return res.text();
}

async function initCaptcha() {
  const params = {
    AccessKeyId: CAPTCHA_AK, Action: "InitCaptchaV3", Format: "JSON", Language: "en",
    Mode: "popup", SceneId: CAPTCHA_SCENE, SignatureMethod: "HMAC-SHA1",
    SignatureNonce: uuidV4(), SignatureVersion: "1.0", Timestamp: new Date().toISOString().replace(/\.\d+Z$/, "Z"),
    UpLang: "true", Version: "2023-03-05",
  };
  const resp = await aliyunRequest(CAPTCHA_INIT, params);
  try { return JSON.parse(resp).CertifyId; } catch { throw new Error("initCaptcha parse fail: " + resp.slice(0, 200)); }
}

// generateArg: urlencode → RC4-like → base64
function generateArg(certifyID) {
  const encoded = encodeURIComponent(certifyID);
  const o = new TextEncoder().encode(encoded);
  const t = rc4Like(o, ARG_CONST, ARG_PERM);
  return btoa(String.fromCharCode(...t));
}

async function computeCaptchaParam(deviceToken) {
  const certifyId = await initCaptcha();
  const argValue = generateArg(certifyId);
  const ct = Date.now();
  const track = JSON.stringify({
    TrackList: { StartTime: ct },
    TrackStartTime: ct,
    VerifyTime: ct + 300,
    arg: argValue,
  });
  const h = aliHash(track, "0000");
  const combined = h + track;
  const compressed = await zlibCompress(new TextEncoder().encode(combined));
  const fb64 = btoa(String.fromCharCode(...compressed));
  const finalVal = btoa(String.fromCharCode(...rc4Like(new TextEncoder().encode(fb64), ENCRYPT_KEY, ARG_PERM)));

  const cvp = JSON.stringify({ certifyId, data: finalVal, deviceToken, sceneId: CAPTCHA_SCENE });
  const params = {
    AccessKeyId: CAPTCHA_AK, Action: "VerifyCaptchaV3", Format: "JSON",
    SignatureMethod: "HMAC-SHA1", SignatureVersion: "1.0",
    Timestamp: new Date().toISOString().replace(/\.\d+Z$/, "Z"), Version: "2023-03-05",
    SceneId: CAPTCHA_SCENE, CertifyId: certifyId, CaptchaVerifyParam: cvp, SignatureNonce: uuidV4(),
  };
  const resp = await aliyunRequest(CAPTCHA_VERIFY, params, { Referer: "" });
  const j = JSON.parse(resp);
  if (!j.Success || !j.Result || !j.Result.VerifyResult) throw new Error("captcha verify failed: " + resp.slice(0, 200));
  const ci = j.Result.certifyId;
  const st = j.Result.securityToken;
  if (!ci || !st) throw new Error("captcha OK but certifyId/securityToken empty");
  const fp = JSON.stringify({ certifyId: ci, isSign: true, sceneId: CAPTCHA_SCENE, securityToken: st });
  return btoa(fp);
}

// ============ Z.AI 会话 ============
async function getGuestSession() {
  const res = await fetch(BASE + "/api/v1/auths/", {
    headers: { Origin: BASE, Referer: BASE + "/", "Content-Type": "application/json", "User-Agent": UA },
  });
  if (!res.ok) throw new Error("guest auth HTTP " + res.status);
  const j = await res.json();
  return { token: j.token, userId: j.id };
}

async function zaiSignature(prompt, token, userId) {
  const ts = String(Date.now());
  const requestId = uuidV4();
  const bucket = Math.floor(Date.now() / 300000);
  const wKey = await hmacSha256Hex(SALT_KEY, String(bucket));
  const sorted = `requestId,${requestId},timestamp,${ts},user_id,${userId}`;
  const promptB64 = b64encode(prompt.trim());
  const dataToSign = `${sorted}|${promptB64}|${ts}`;
  return hmacSha256Hex(wKey, dataToSign);
}

async function chatWithZAI(prompt, model, deviceToken, history = []) {
  const { token, userId } = await getGuestSession();
  const signature = await zaiSignature(prompt, token, userId);
  const captchaParam = await computeCaptchaParam(deviceToken);
  const messages = [...history, { role: "user", content: prompt }];
  const body = {
    model,
    chatId: "",
    messages,
    signature_prompt: prompt,
    stream: true,
    captcha_verify_param: captchaParam,
    features: { image_generation: false, web_search: false, auto_web_search: false, preview_mode: false, flags: [], enable_thinking: false },
  };
  const res = await fetch(BASE + "/api/v2/chat/completions", {
    method: "POST",
    headers: {
      authorization: "Bearer " + token, "content-type": "application/json",
      "x-fe-Version": FE_VERSION, "x-region": "overseas", "x-signature": signature,
      Origin: BASE, Referer: BASE + "/", "User-Agent": UA,
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error("chat HTTP " + res.status + ": " + (await res.text()).slice(0, 200));
  return { res, token, userId };
}

// ============ KV deviceToken 池 ============
async function takeDeviceToken(env) {
  const kv = env.ZAI_TOKENS;
  if (!kv) throw new Error("ZAI_TOKENS KV 未绑定");
  const list = await kv.list({ limit: 100 });
  if (!list.keys.length) throw new Error("deviceToken 池空 — 先跑 collect-tokens.mjs 采集");
  const k = list.keys[0].name;
  const tok = await kv.get(k);
  await kv.delete(k);
  return tok;
}

// ============ HTTP 服务 ============
function cors() { return { "Access-Control-Allow-Origin": "*", "Access-Control-Allow-Methods": "GET, POST, OPTIONS", "Access-Control-Allow-Headers": "Content-Type, Authorization" }; }
function json(data, status = 200, extra = {}) { return new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json; charset=utf-8", ...cors(), ...extra } }); }

const MODELS = [
  { id: "glm-4.7", name: "GLM-4.7 (guest)", free: true },
  { id: "GLM-5-Turbo", name: "GLM-5-Turbo", free: false },
  { id: "GLM-5v-Turbo", name: "GLM-5v-Turbo", free: false },
  { id: "GLM-5.1", name: "GLM-5.1", free: false },
  { id: "glm-5.2", name: "GLM-5.2", free: false },
];

async function handleChat(req, env) {
  let body;
  try { body = await req.json(); } catch { return json({ error: { message: "invalid JSON" } }, 400); }
  const model = body.model || DEFAULT_MODEL;
  const messages = Array.isArray(body.messages) ? body.messages : [];
  const prompt = messages.length ? (messages[messages.length - 1].content || "") : "";
  if (!prompt) return json({ error: { message: "empty prompt" } }, 400);
  const history = messages.slice(0, -1).map(m => ({ role: m.role, content: m.content }));

  try {
    const deviceToken = await takeDeviceToken(env);
    const { res, token, userId } = await chatWithZAI(prompt, model, deviceToken, history);

    if (body.stream === true) {
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      const out = new Response(new ReadableStream({
        async start(controller) {
          let buf = "";
          let content = "";
          const send = (obj) => controller.enqueue(new TextEncoder().encode("data: " + JSON.stringify(obj) + "\n\n"));
          try {
            while (true) {
              const { done, value } = await reader.read();
              if (done) break;
              buf += decoder.decode(value, { stream: true });
              const lines = buf.split("\n");
              buf = lines.pop();
              for (const line of lines) {
                if (!line.startsWith("data: ")) continue;
                const raw = line.slice(6);
                if (raw === "[DONE]") continue;
                try {
                  const ev = JSON.parse(raw);
                  const delta = ev?.data?.delta || ev?.delta || "";
                  content += delta;
                  send({ id: "chatcmpl-zai", object: "chat.completion.chunk", created: Math.floor(Date.now() / 1000), model, choices: [{ index: 0, delta: { content: delta }, finish_reason: null }] });
                } catch {}
              }
            }
            send({ id: "chatcmpl-zai", object: "chat.completion.chunk", created: Math.floor(Date.now() / 1000), model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }] });
            controller.enqueue(new TextEncoder().encode("data: [DONE]\n\n"));
          } catch (e) {
            controller.enqueue(new TextEncoder().encode("data: " + JSON.stringify({ error: { message: e.message } }) + "\n\n"));
          } finally {
            controller.close();
          }
        },
      }), { headers: { "Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-cache", ...cors() } });
      return out;
    }

    // 非流式: 累积完整内容
    const text = await res.text();
    let content = "";
    for (const line of text.split("\n")) {
      if (!line.startsWith("data: ")) continue;
      const raw = line.slice(6);
      if (raw === "[DONE]") continue;
      try { const ev = JSON.parse(raw); content += ev?.data?.delta || ev?.delta || ""; } catch {}
    }
    return json({
      id: "chatcmpl-zai", object: "chat.completion", created: Math.floor(Date.now() / 1000), model,
      choices: [{ index: 0, message: { role: "assistant", content }, finish_reason: "stop" }],
      usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 },
    });
  } catch (e) {
    return json({ error: { message: e.message, hint: "Z.AI guest 通道错误" } }, 502);
  }
}

export default {
  async fetch(req, env) {
    const url = new URL(req.url);
    const p = url.pathname.replace(/\/+$/, "");
    const auth = req.headers.get("Authorization") || "";
    if (env.ZAI_AUTH_KEY && auth !== "Bearer " + env.ZAI_AUTH_KEY) return json({ error: { message: "invalid api key" } }, 401);
    if (req.method === "OPTIONS") return new Response(null, { status: 204, headers: cors() });

    if (p === "/healthz" || p === "/") {
      let tokens = 0;
      try { const l = await env.ZAI_TOKENS.list({ limit: 1 }); tokens = l.keys.length ? 1 : 0; } catch {}
      return json({ ok: true, service: "zai2api", has_tokens: !!tokens, model: DEFAULT_MODEL });
    }
    if (p === "/v1/models") {
      return json({ object: "list", data: MODELS.map(m => ({ id: m.id, object: "model", owned_by: "z-ai", free: m.free })) });
    }
    if (p === "/v1/chat/completions" && req.method === "POST") return handleChat(req, env);
    return json({ error: { message: "Not Found" } }, 404);
  },
};
