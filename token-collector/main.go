// token-collector/main.go — Device-token collector for Aliyun Captcha.
// Go port of .assets/token-helper-js/index.js (Playwright → chromedp).
//
// Launches headless Chrome, navigates to chat.z.ai, triggers the token
// endpoint, then calls window.z_um.getToken() in a loop to collect device
// tokens. Saves them to tokens.sqlite for the captcha solver.
//
// Usage:
//
//	go run ./token-collector/
//	go build -o token-collector.exe ./token-collector/
//	token-collector.exe --headless=false        # visible browser
//	token-collector.exe --count 500 --out my_tokens.sqlite
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	_ "modernc.org/sqlite"
)

const (
	maxTokens           = 1250
	defaultTokens       = 750
	maxRetries          = 5
	tokenCollectTimeout = 360 * time.Second
	zaiURL              = "https://chat.z.ai"
)

func main() {
	var (
		count    int
		outPath  string
		headless bool
	)
	flag.IntVar(&count, "count", defaultTokens, fmt.Sprintf("Number of tokens to collect (max %d)", maxTokens))
	flag.StringVar(&outPath, "out", "tokens.sqlite", "Output SQLite database path")
	flag.BoolVar(&headless, "headless", true, "Run browser headless")
	flag.Parse()

	if count <= 0 {
		count = defaultTokens
	}
	if count > maxTokens {
		fmt.Printf("Capping to max %d.\n", maxTokens)
		count = maxTokens
	}

	// Interactive prompt if no --count flag given explicitly
	countChanged := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "count" {
			countChanged = true
		}
	})
	if !countChanged {
		reader := bufio.NewReader(os.Stdin)
		fmt.Printf("How many tokens to collect? [default: %d, max: %d] ", defaultTokens, maxTokens)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			if n, err := strconv.Atoi(line); err == nil && n > 0 {
				count = n
				if count > maxTokens {
					count = maxTokens
				}
			}
		}
	}

	fmt.Printf("\nCollecting %d tokens\n", count)

	// Chrome allocator options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", headless),
	)
	// 代理支持: 走低风险出口, 绕过数据中心 IP 风控
	if proxy := os.Getenv("ZAI_PROXY"); proxy != "" {
		fmt.Printf("  Using proxy: %s\n", proxy)
		opts = append(opts, chromedp.ProxyServer(proxy))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Listen for any JavaScript dialogs (like beforeunload) and automatically accept them
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if _, ok := ev.(*page.EventJavascriptDialogOpening); ok {
			go chromedp.Run(ctx, page.HandleJavaScriptDialog(true))
		}
	})

	var success bool

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("\nAttempt %d of %d\n", attempt, maxRetries)

		// On retry, try to clear the chat input before navigating,
		// which acts as a secondary measure against "unsaved changes" warnings.
		if attempt > 1 {
			chromedp.Run(ctx, chromedp.Evaluate(`
				var input = document.querySelector("#chat-input");
				if (input) {
					input.value = "";
				}
			`, nil))
		}

		err := tryCollect(ctx, count, outPath)
		if err != nil {
			fmt.Printf("  Attempt %d failed: %v\n", attempt, err)
			if attempt == maxRetries {
				fmt.Println("  All retries exhausted.")
				log.Printf("Error: %v", err)
				waitForEnter()
				os.Exit(1)
			}
			fmt.Println("  Retrying with a fresh page load...")
			continue
		}
		success = true
		break
	}

	if !success {
		fmt.Println("Failed after maximum retries.")
		waitForEnter()
		os.Exit(1)
	}

	fmt.Println("\nScript finished successfully.")
}

func tryCollect(ctx context.Context, total int, outPath string) error {
	// Navigate to Z.AI
	fmt.Println("  Navigating to chat.z.ai...")
	if err := chromedp.Run(ctx,
		chromedp.Navigate(zaiURL),
		chromedp.WaitVisible(`#chat-input`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("page load failed: %w", err)
	}
	fmt.Println("  Chat input found")

	// Fill textarea and click send
	if err := chromedp.Run(ctx,
		chromedp.SendKeys(`#chat-input`, "__", chromedp.ByQuery),
		chromedp.Click(`#send-message-button`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("send click failed: %w", err)
	}
	fmt.Println("  Send clicked")

	// Wait for window.z_um.getToken to be ready — the page injects the captcha
	// SDK async after the first send, so a fixed sleep is flaky under load.
	fmt.Println("  Waiting for token endpoint...")
	waitCtx, waitCancel := context.WithTimeout(ctx, 90*time.Second)
	defer waitCancel()
	if err := waitForZUM(waitCtx); err != nil {
		return fmt.Errorf("token endpoint not ready: %w", err)
	}

	// Collect tokens with timeout
	fmt.Println("  Collecting tokens...")
	collectCtx, cancel := context.WithTimeout(ctx, tokenCollectTimeout)
	defer cancel()

	t0 := time.Now()

	// ⚠️ 顺序采集 + 单次超时: 15 并发的 Promise.all 会卡死页面 JS 主线程;
	// 纯顺序无超时的话, 单个 getToken() 挂住不 resolve 会拖到整体 300s 超时。
	// 方案: 顺序 await + Promise.race 给每次调用 10s 上限, 挂住的跳过不拖垮整批。
	jsExpr := fmt.Sprintf(`(async () => {
		const total = %d;
		const perCallTimeout = 10000;
		const withTimeout = (p) => Promise.race([
			Promise.resolve(p),
			new Promise(r => setTimeout(() => r(null), perCallTimeout))
		]);
		const out = new Array(total);
		for (let i = 0; i < total; i++) {
			try {
				out[i] = await withTimeout(window.z_um.getToken());
			} catch (e) {
				out[i] = null;
			}
		}
		return out;
	})()`, total)

	// Use runtime.Evaluate with awaitPromise + returnByValue
	var tokens []string
	err := chromedp.Run(collectCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			result, exception, err := runtime.Evaluate(jsExpr).
				WithAwaitPromise(true).
				WithReturnByValue(true).
				Do(ctx)
			if err != nil {
				return err
			}
			if exception != nil {
				return fmt.Errorf("JS exception: %s", exception.Text)
			}
			if result == nil || result.Value == nil {
				return fmt.Errorf("empty result from getToken")
			}
			return json.Unmarshal(result.Value, &tokens)
		}),
	)
	if err != nil {
		return fmt.Errorf("token collection failed: %w", err)
	}

	// Filter out empty or invalid tokens
	// ⚠️ getToken() 返回的是 base64 编码的阿里云 deviceToken, 解码后以 "SG_WEB#" 开头
	// base64("SG_WEB#") = "U0dfV0VC" — 直接检查 base64 前缀 (勿用明文前缀, 会全部误拒!)
	var validTokens []string
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if !strings.HasPrefix(tok, "U0dfV0VC") {
			continue
		}
		if len(tok) < 30 {
			continue // 真 token 远长于此; 短 token 无效
		}
		validTokens = append(validTokens, tok)
	}

	if len(validTokens) == 0 {
		return fmt.Errorf("no valid tokens collected (received empty values)")
	}

	elapsed := time.Since(t0).Seconds()
	fmt.Printf("  Collected %d valid tokens in %.2fs\n", len(validTokens), elapsed)

	// Save to SQLite
	fmt.Println("  Building SQLite database...")
	if err := saveTokens(outPath, validTokens); err != nil {
		return fmt.Errorf("save failed: %w", err)
	}

	info, _ := os.Stat(outPath)
	sizeKB := 0.0
	if info != nil {
		sizeKB = float64(info.Size()) / 1024
	}
	fmt.Printf("  Saved: %s (%.1f KB)\n", outPath, sizeKB)

	return nil
}

// waitForZUM polls until window.z_um.getToken is defined. Z.AI injects the
// captcha SDK async after the first send; a fixed sleep misses it under load.
// ponytail: 500ms poll — z_um appears in ~2-5s; 30s deadline (caller) is the ceiling.
func waitForZUM(ctx context.Context) error {
	for {
		var ready bool
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(`typeof window.z_um !== 'undefined' && typeof window.z_um.getToken === 'function'`, &ready),
		); err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// saveTokens appends tokens to the SQLite database.
// ⚠️ 不要 os.Remove 重建 — zai-server 持有打开句柄, 删除重建会导致 readonly database (1032)
// busy_timeout: 多进程(collector+server)写同一库时避免 SQLITE_BUSY
func saveTokens(path string, tokens []string) error {
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, token TEXT UNIQUE);"); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO tokens (token) VALUES (?);")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, tok := range tokens {
		if _, err := stmt.Exec(tok); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// waitForEnter pauses before exit so fatal errors stay visible in the
// console window (e.g. when the .exe is double-clicked on Windows).
// Skipped when stdin is piped/redirected, so automation isn't blocked.
func waitForEnter() {
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) == 0 {
		return // not interactive (piped/redirected) — don't block
	}
	fmt.Fprintln(os.Stderr, "\nPress Enter to exit...")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
