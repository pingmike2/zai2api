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
 *
 * 诊断：失败时自动截图到 ./debug-*.png，方便排障（验证墙/登录墙/验证码）。
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
const MAX_WAIT = parseInt(getArg("--wait", "90"), 10); // 等 getToken 就绪的最长秒数

async function debugShot(page, tag) {
  try {
    await page.screenshot({ path: `debug-${tag}.png`, fullPage: false });
    console.log(`[collect] 📸 截图已存 debug-${tag}.png`);
    // 打印页面关键信息帮助判断
    const info = await page.evaluate(() => ({
      url: location.href,
      title: document.title,
      hasChatInput: !!document.querySelector("#chat-input"),
      hasLogin: !!document.querySelector("input[type=email], input[type=password]"),
      bodyText: document.body?.innerText?.slice(0, 200) || "",
    }));
    console.log("[collect] 页面状态:", JSON.stringify(info));
  } catch {}
}

const browser = await chromium.launch({ headless: HEADLESS, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
const page = await browser.newPage({
  userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36",
  viewport: { width: 1280, height: 900 },
  locale: "en-US",
});

// 接受可能的 beforeunload 弹窗
page.on("dialog", (d) => d.accept().catch(() => {}));

let tokens = [];

try {
  console.log("[collect] 打开 chat.z.ai ...");
  await page.goto("https://chat.z.ai", { waitUntil: "domcontentloaded", timeout: 60000 });

  // 等待输入框出现（可能被验证墙挡住）
  let hasInput = false;
  for (let i = 0; i < 30; i++) {
    hasInput = await page.locator("#chat-input").count() > 0;
    if (hasInput) break;
    // 如果出现验证/登录元素, 截图后重试导航
    await page.waitForTimeout(2000);
  }

  if (!hasInput) {
    await debugShot(page, "no-input");
    // 尝试刷新一次 (可能验证墙在等 JS)
    console.log("[collect] 输入框未出现, 刷新重试...");
    await page.goto("https://chat.z.ai", { waitUntil: "domcontentloaded", timeout: 60000 });
    await page.waitForTimeout(10000);
    hasInput = await page.locator("#chat-input").count() > 0;
  }

  if (!hasInput) {
    await debugShot(page, "still-no-input");
    throw new Error("chat 输入框未出现 (可能被验证墙/登录墙挡住)");
  }

  // 发一条消息触发验证码 SDK
  await page.locator("#chat-input").first().fill("__");
  await page.keyboard.press("Enter");
  console.log("[collect] 已发送触发消息，等待 z_um.getToken ...");

  // 等待 window.z_um.getToken 就绪
  let ready = false;
  for (let i = 0; i < MAX_WAIT; i++) {
    ready = await page.evaluate(() => typeof window.z_um?.getToken === "function");
    if (ready) break;
    await page.waitForTimeout(1000);
  }
  if (!ready) {
    await debugShot(page, "no-zum");
    throw new Error("z_um.getToken 未就绪（可能被验证码/登录墙挡住）");
  }
  console.log("[collect] ✅ getToken 就绪，开始采集 ...");

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
} catch (e) {
  console.error("[collect] ❌", e.message);
  await debugShot(page, "error").catch(() => {});
  await browser.close().catch(() => {});
  process.exit(1);
}

await browser.close().catch(() => {});

if (!tokens.length) {
  console.error("[collect] ❌ 一个 token 都没采到");
  process.exit(1);
}

// 写成 wrangler kv bulk put 格式 (数组, 非对象)
const kvArr = tokens.map((t, i) => ({ key: `t${i}_${t.slice(0, 8)}`, value: t }));
fs.writeFileSync(OUT, JSON.stringify(kvArr, null, 2));
console.log(`\n[collect] ✅ 采集 ${tokens.length} 个 deviceToken → ${OUT}`);
console.log(`\n部署到 KV：\n  npx wrangler kv bulk put --binding=ZAI_TOKENS --remote ${OUT}`);
