#!/usr/bin/env node
// token-collector-js/index.js — Playwright deviceToken 采集器
// 原版作者用 Playwright 采 token 成功; playwright-extra + stealth 插件反检测能力远超 chromedp
// 用法: node index.js --count 150 --out /data/tokens.sqlite [--headless true] [--proxy socks5://...]
const { chromium } = require('playwright-extra');
const StealthPlugin = require('puppeteer-extra-plugin-stealth');
const fs = require('fs');
const path = require('path');
const sqlite3 = require('better-sqlite3');

// 启用 stealth 反检测
chromium.use(StealthPlugin());

const ZAI_URL = 'https://chat.z.ai';

function parseArgs() {
  const args = process.argv.slice(2);
  const get = (k, d) => {
    const i = args.indexOf(k);
    return i >= 0 && args[i + 1] ? args[i + 1] : d;
  };
  return {
    count: parseInt(get('--count', '150'), 10),
    out: get('--out', '/data/tokens.sqlite'),
    headless: get('--headless', 'true') !== 'false',
    proxy: get('--proxy', process.env.ZAI_PROXY || ''),
  };
}

async function launch(opts) {
  const launchOpts = {
    headless: opts.headless,
    executablePath: '/usr/bin/chromium',
    args: [
      '--no-sandbox',
      '--disable-dev-shm-usage',
      '--disable-blink-features=AutomationControlled',
      '--disable-infobars',
    ],
  };
  if (opts.proxy) {
    launchOpts.proxy = { server: opts.proxy };
  }
  return chromium.launch(launchOpts);
}

// 保存 token 到 SQLite (追加模式, 不删库重建!)
function saveTokens(dbPath, tokens) {
  const dir = path.dirname(dbPath);
  fs.mkdirSync(dir, { recursive: true });
  const db = new sqlite3(dbPath);
  db.pragma('journal_mode = WAL');
  db.pragma('busy_timeout = 10000');
  db.exec('CREATE TABLE IF NOT EXISTS tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, token TEXT UNIQUE)');
  const stmt = db.prepare('INSERT OR IGNORE INTO tokens (token) VALUES (?)');
  const tx = db.transaction((toks) => {
    for (const t of toks) stmt.run(t);
  });
  tx(tokens);
  db.close();
}

async function collect(browser, opts) {
  const ctx = await browser.newContext({
    viewport: { width: 1280, height: 900 },
    userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36',
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
  });

  // stealth 深度注入 (即使有 stealth 插件, 再加一层保险)
  await ctx.addInitScript(() => {
    Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
    window.chrome = window.chrome || { runtime: {} };
    Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3, 4, 5] });
    Object.defineProperty(navigator, 'languages', { get: () => ['zh-CN', 'zh', 'en-US', 'en'] });
    const origQuery = window.navigator.permissions.query;
    window.navigator.permissions.query = (parameters) => (
      parameters.name === 'notifications'
        ? Promise.resolve({ state: Notification.permission })
        : origQuery(parameters)
    );
  });

  const page = await ctx.newPage();

  console.log('  Navigating to chat.z.ai...');
  await page.goto(ZAI_URL, { waitUntil: 'domcontentloaded', timeout: 60000 });
  await page.waitForSelector('#chat-input', { timeout: 60000 });
  console.log('  Chat input found');

  // 触发验证码 SDK 加载
  // ⚠️ 用 evaluate 直接触发 click — Playwright 原生 click 会被页面覆盖层拦截超时
  await page.fill('#chat-input', '__');
  await page.evaluate(() => {
    const btn = document.querySelector('#send-message-button');
    if (btn) btn.click();
  });
  console.log('  Send clicked');

  // 等待 window.z_um.getToken 就绪
  console.log('  Waiting for token endpoint...');
  try {
    await page.waitForFunction(
      () => typeof window.z_um !== 'undefined' && typeof window.z_um.getToken === 'function',
      { timeout: 90000 }
    );
  } catch (e) {
    throw new Error('token endpoint not ready: ' + e.message);
  }

  // 顺序采集 + 单次超时 (挂住不拖垮整批)
  console.log('  Collecting tokens...');
  const t0 = Date.now();
  const total = opts.count;
  const perCallTimeout = 10000;
  const tokens = await page.evaluate(async ({ total, perCallTimeout }) => {
    const withTimeout = (p) => Promise.race([
      Promise.resolve(p),
      new Promise((r) => setTimeout(() => r(null), perCallTimeout)),
    ]);
    const out = [];
    for (let i = 0; i < total; i++) {
      try {
        out.push(await withTimeout(window.z_um.getToken()));
      } catch (e) {
        out.push(null);
      }
    }
    return out;
  }, { total, perCallTimeout });

  const elapsed = ((Date.now() - t0) / 1000).toFixed(2);

  // 过滤有效 token (base64 编码, 解码后以 SG_WEB# 开头)
  const valid = tokens.filter((t) => {
    if (!t || typeof t !== 'string') return false;
    const s = t.trim();
    if (!s.startsWith('U0dfV0VC')) return false; // base64("SG_WEB#") 前缀
    if (s.length < 30) return false;
    return true;
  });

  console.log(`  Collected ${valid.length} valid tokens in ${elapsed}s`);
  if (valid.length === 0) {
    throw new Error('no valid tokens collected (received empty values)');
  }

  // 保存
  console.log('  Building SQLite database...');
  saveTokens(opts.out, valid);
  const sizeKB = (fs.statSync(opts.out).size / 1024).toFixed(1);
  console.log(`  Saved: ${opts.out} (${sizeKB} KB)`);
  console.log('\nScript finished successfully.');
  await browser.close();
  process.exit(0);
}

(async () => {
  const opts = parseArgs();
  console.log(`Collecting ${opts.count} tokens`);
  if (opts.proxy) console.log(`  Using proxy: ${opts.proxy.replace(/:\/\/[^@]+@/, '://***@')}`);
  console.log('');
  const browser = await launch(opts);
  for (let attempt = 1; attempt <= 5; attempt++) {
    console.log(`\nAttempt ${attempt} of 5`);
    try {
      await collect(browser, opts);
      return;
    } catch (err) {
      console.log(`  Attempt ${attempt} failed: ${err.message}`);
      if (attempt === 5) {
        console.log('  All retries exhausted.');
        process.exit(1);
      }
      console.log('  Retrying with a fresh page load...');
    }
  }
})().catch((err) => {
  console.error('Fatal:', err.message);
  process.exit(1);
});
