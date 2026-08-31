/**
 * collect-tokens.mjs — 采集 Z.AI 的阿里云验证码 deviceToken 池
 *
 * 原理：无头 Chrome 打开 chat.z.ai，发一条消息触发验证码 SDK 加载，
 * 然后循环调用 window.z_um.getToken() 收集 deviceToken（一次性，每 token 用一次）。
 *
 * 用法：
 *   npm i playwright          # 一次性
 *   npx playwright install chromium
 *   node collect-tokens.mjs --count 750 --out tokens.json
 *
 * 采集完成后：
 *   npx wrangler kv bulk put --binding=ZAI_TOKENS tokens.json
 */

import { chromium } from "playwright";
import fs from "fs";

const args = process.argv.slice(2);
const getArg = (k, def) => {
  const i = args.indexOf(k);
  return i >= 0 ? args[i + 1] : def;
};
const COUNT = parseInt(getArg("--count", "750"), 10);
const OUT = getArg("--out", "tokens.json");
const HEADLESS = getArg("--headless", "true") !== "false";

const browser = await chromium.launch({ headless: HEADLESS, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
const page = await browser.newPage({ userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36" });

// 接受可能的 beforeunload 弹窗
page.on("dialog", (d) => d.accept().catch(() => {}));

console.log("[collect] 打开 chat.z.ai ...");
await page.goto("https://chat.z.ai", { waitUntil: "domcontentloaded", timeout: 60000 });

// 找输入框并发一条消息触发验证码 SDK
const input = page.locator("#chat-input").first();
await input.waitFor({ timeout: 60000 });
await input.fill("__");
await page.keyboard.press("Enter");
console.log("[collect] 已发送触发消息，等待 z_um.getToken ...");

// 等待 window.z_um.getToken 就绪
let ready = false;
for (let i = 0; i < 60; i++) {
  ready = await page.evaluate(() => typeof window.z_um?.getToken === "function");
  if (ready) break;
  await page.waitForTimeout(1000);
}
if (!ready) {
  console.error("[collect] ❌ z_um.getToken 未就绪（可能被验证码/登录墙挡住）");
  await browser.close();
  process.exit(1);
}
console.log("[collect] ✅ getToken 就绪，开始采集 ...");

const tokens = [];
for (let i = 0; i < COUNT; i++) {
  try {
    const tok = await page.evaluate(() => {
      const t = window.z_um.getToken();
      return t && typeof t.then === "function" ? t : Promise.resolve(t);
    });
    const s = String(tok || "").trim();
    if (s && s !== "null" && s !== "undefined") tokens.push(s);
  } catch {}
  if ((i + 1) % 50 === 0) {
    console.log(`  ...已采集 ${tokens.length} / ${COUNT}`);
    await page.waitForTimeout(100);
  }
}

await browser.close();

if (!tokens.length) {
  console.error("[collect] ❌ 一个 token 都没采到");
  process.exit(1);
}

// 写成 wrangler kv bulk put 格式
const kv = {};
tokens.forEach((t, i) => { kv[`t${i}_${t.slice(0, 8)}`] = t; });
fs.writeFileSync(OUT, JSON.stringify(kv, null, 2));
console.log(`\n[collect] ✅ 采集 ${tokens.length} 个 deviceToken → ${OUT}`);
console.log(`\n部署到 KV：\n  npx wrangler kv bulk put --binding=ZAI_TOKENS ${OUT}`);
