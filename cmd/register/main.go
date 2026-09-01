package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"zai-proxy/internal"
)

// TempMailProvider 临时邮箱服务
type TempMailProvider struct {
	Name        string
	GenerateURL string
	CheckURL    string
	Headers     map[string]string
}

var tempMailProviders = []TempMailProvider{
	{
		Name:        "chatgpt.org.uk",
		GenerateURL: "https://mail.chatgpt.org.uk/api/generate-email",
		CheckURL:    "https://mail.chatgpt.org.uk/api/emails?email=%s",
		Headers: map[string]string{
			"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			"Referer":    "https://mail.chatgpt.org.uk",
		},
	},
}

// SliderTrack 滑块轨迹点
type SliderTrack struct {
	X    int   `json:"x"`
	Y    int   `json:"y"`
	Time int64 `json:"t"`
}

// GenerateSliderTrack 生成滑块轨迹
// 公式: y = 14.7585 * x^0.5190 - 3.9874
func GenerateSliderTrack(distance int) []SliderTrack {
	tracks := make([]SliderTrack, 0)
	startTime := time.Now().UnixMilli()

	// 初始点
	tracks = append(tracks, SliderTrack{X: 0, Y: 0, Time: 0})

	currentX := 0.0
	totalTime := int64(0)

	// 使用贝塞尔曲线模拟人手滑动
	steps := 30 + rand.Intn(20) // 30-50步

	for i := 1; i <= steps; i++ {
		progress := float64(i) / float64(steps)

		// 使用缓动函数模拟加速减速
		// easeOutQuad: 1 - (1 - t)^2
		easedProgress := 1 - math.Pow(1-progress, 2)

		targetX := float64(distance) * easedProgress

		// 计算Y偏移，使用给定公式: y = 14.7585 * x^0.5190 - 3.9874
		// 添加随机抖动
		baseY := 14.7585*math.Pow(targetX, 0.5190) - 3.9874
		yOffset := baseY*0.1 + float64(rand.Intn(5)-2)

		// 时间增量，模拟人类操作的不均匀性
		timeStep := int64(20 + rand.Intn(30)) // 20-50ms
		totalTime += timeStep

		currentX = targetX

		tracks = append(tracks, SliderTrack{
			X:    int(currentX),
			Y:    int(yOffset),
			Time: totalTime,
		})
	}

	// 确保最后一个点到达目标
	tracks = append(tracks, SliderTrack{
		X:    distance,
		Y:    rand.Intn(3) - 1,
		Time: totalTime + int64(50+rand.Intn(30)),
	})

	_ = startTime
	return tracks
}

// GenerateUsername 生成随机用户名
func GenerateUsername() string {
	chars := "abcdefghijklmnopqrstuvwxyz0123456789"
	length := 8 + rand.Intn(8) // 8-15字符
	result := make([]byte, length)
	for i := range result {
		result[i] = chars[rand.Intn(len(chars))]
	}
	return string(result)
}

// GeneratePassword 生成随机密码
func GeneratePassword() string {
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	length := 12 + rand.Intn(6) // 12-17字符
	result := make([]byte, length)
	for i := range result {
		result[i] = chars[rand.Intn(len(chars))]
	}
	return string(result)
}

// HTTPClient 基于 internal 的 tls-client（与 Chrome 133 TLS/H2/UA 指纹一致）
type HTTPClient struct {
	client tls_client.HttpClient
}

func NewHTTPClient() *HTTPClient {
	c, err := internal.TLSHTTPClient(30 * time.Second)
	if err != nil {
		panic("tls-client: " + err.Error())
	}
	return &HTTPClient{client: c}
}

func (c *HTTPClient) SetDefaultHeaders(req *fhttp.Request) {
	internal.ApplyBrowserFingerprintHeaders(req.Header)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("DNT", "1")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
}

// GetTempEmail 获取临时邮箱
func (c *HTTPClient) GetTempEmail() (string, error) {
	provider := tempMailProviders[0]

	req, err := fhttp.NewRequest("GET", provider.GenerateURL, nil)
	if err != nil {
		return "", err
	}

	for k, v := range provider.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取临时邮箱失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// 尝试两种格式解析
	var result1 struct {
		Email string `json:"email"`
	}
	var result2 struct {
		Success bool `json:"success"`
		Data    struct {
			Email string `json:"email"`
		} `json:"data"`
	}

	// 先尝试 {data: {email: ...}} 格式
	if err := json.Unmarshal(body, &result2); err == nil && result2.Data.Email != "" {
		return result2.Data.Email, nil
	}

	// 再尝试 {email: ...} 格式
	if err := json.Unmarshal(body, &result1); err == nil && result1.Email != "" {
		return result1.Email, nil
	}

	return "", fmt.Errorf("获取邮箱为空, body: %s", string(body))
}

// CheckEmail 检查邮箱获取验证token
func (c *HTTPClient) CheckEmail(email string) (string, error) {
	provider := tempMailProviders[0]
	url := fmt.Sprintf(provider.CheckURL, email)

	maxRetries := 30
	for i := 0; i < maxRetries; i++ {
		req, err := fhttp.NewRequest("GET", url, nil)
		if err != nil {
			return "", err
		}

		for k, v := range provider.Headers {
			req.Header.Set(k, v)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// 解析邮件列表 - 适配新API格式
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Emails []struct {
					Subject     string `json:"subject"`
					Content     string `json:"content"`
					HtmlContent string `json:"html_content"`
				} `json:"emails"`
			} `json:"data"`
		}

		if err := json.Unmarshal(body, &response); err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		// 查找验证邮件
		for _, mail := range response.Data.Emails {
			if strings.Contains(strings.ToLower(mail.Subject), "verify") ||
				strings.Contains(mail.Subject, "验证") ||
				strings.Contains(mail.Subject, "z.ai") {
				// 从邮件内容提取token
				token := extractTokenFromEmail(mail.HtmlContent)
				if token == "" {
					token = extractTokenFromEmail(mail.Content)
				}
				if token != "" {
					return token, nil
				}
			}
		}

		fmt.Printf("  等待验证邮件... (%d/%d)\n", i+1, maxRetries)
		time.Sleep(3 * time.Second)
	}

	return "", fmt.Errorf("等待验证邮件超时")
}

// FinishSignup 通过HTTP请求完成注册
func (c *HTTPClient) FinishSignup(email, password, verifyToken string) (string, error) {
	// Step 1: 验证邮箱
	verifyData := fmt.Sprintf(`{"username":"%s","email":"%s","token":"%s"}`, email, email, verifyToken)
	req, _ := fhttp.NewRequest("POST", "https://chat.z.ai/api/v1/auths/verify_email", strings.NewReader(verifyData))
	req.Header.Set("Content-Type", "application/json")
	c.SetDefaultHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("验证邮箱请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 获取cookie中的token
	var tempToken string
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "token" {
			tempToken = cookie.Value
			break
		}
	}

	// Step 2: 完成注册
	profileImage := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAGQAAABkCAYAAABw4pVUAAACtUlEQVR4AeyaPUplQRBGLy+bGaOZSWYXs41h3IEYiJF7UtyEP4kG7sBYTATBRAPxoYK/kaDQFvJ1V1e1HuGCdllf1TuHTi5vdrX+84knD4PZxE8qAghJpWOaEIKQZASSrcMNQUgyAsnW4YYgJBmBZOtwQ76EkGQfcqR1uCHJbCEEIckIJFuHG4KQZASSrcMNQUgyAsnW4YYgJBmBZOuMdEOSofNZByE+XOVUhMjofBoR4sNVTkWIjM6nESE+XOVUhMjofBoR4sNVTkWIjM6nESE+XOVUhMjofBoR4sNVTkWIjM6nMb2QhZWzaWH1otnzY+nIh2Sj1PRCGn3OYWIQkkzVUEKeLo+n273luudgLZmCt+uMJeRuPt2f7FQ9D6f7bwkk+2soIcnYuayDEBeseihCdHZWp1xDiIzOpxEhPlzlVITI6HwaEeLDVU4dSsjs99+qd1rfF3dlUL0ahxLSC0rkHIRE0i/MHkrI4/nhNN/4JT/XW/8KCHIdDSUkFzqfbRDiw1VONYXIqTTKBBAio/NpRIgPVzkVITI6n0aE+HCVUxEio/NpRIgPVzl1KCG1LxdfvnD37f+WDMy7cSgh3jAy5CMkg4VXOwQIeTX9A7/ON//ILxPfexF5s734gckx/5JeSAyWuKkIiWNfnIyQIpa4Q4TEsS9ORkgRS9whQuLYFycjpIgl7hAhceyLkxFSxBJ3+GmExCFsOxkhbXlWpyGkGmHbAIS05VmdhpBqhG0DENKWZ3UaQqoRtg1ASFue1WkIqUbYNgAhbXlWpyHERNi/iJD+zM2JCDHx9C8ipD9zcyJCTDz9iwjpz9yciBATT/8iQvozNycixMTTv4iQ/szNiQgx8fgUrVSEWHQCaggJgG6NRIhFJ6CGkADo1kiEWHQCaggJgG6NRIhFJ6CGkADo1shnAAAA//+Le9XMAAAABklEQVQDAJLb6FjT4DiyAAAAAElFTkSuQmCC"
	signupData := fmt.Sprintf(`{"username":"%s","email":"%s","token":"%s","password":"%s","profile_image_url":"%s","sso_redirect":null}`, email, email, verifyToken, password, profileImage)

	req2, _ := fhttp.NewRequest("POST", "https://chat.z.ai/api/v1/auths/finish_signup", strings.NewReader(signupData))
	req2.Header.Set("Content-Type", "application/json")
	if tempToken != "" {
		req2.Header.Set("Cookie", "token="+tempToken)
	}
	c.SetDefaultHeaders(req2)

	resp2, err := c.client.Do(req2)
	if err != nil {
		return "", fmt.Errorf("完成注册请求失败: %v", err)
	}
	defer resp2.Body.Close()

	body, _ := io.ReadAll(resp2.Body)

	// 解析响应获取token
	var result struct {
		Success bool `json:"success"`
		User    struct {
			Token string `json:"token"`
		} `json:"user"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %v, body: %s", err, string(body))
	}

	if !result.Success || result.User.Token == "" {
		return "", fmt.Errorf("注册失败: %s", string(body))
	}

	return result.User.Token, nil
}

// extractTokenFromEmail 从邮件内容提取token
func extractTokenFromEmail(content string) string {
	// 处理HTML编码
	content = strings.ReplaceAll(content, "&amp;", "&")

	// 查找 token= 参数 (UUID格式: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)
	if idx := strings.Index(content, "token="); idx != -1 {
		start := idx + 6
		end := start
		// UUID格式只包含字母数字和连字符
		for end < len(content) {
			c := content[end]
			if (c >= 'a' && c <= 'f') || (c >= '0' && c <= '9') || c == '-' {
				end++
			} else {
				break
			}
		}
		if end > start {
			return content[start:end]
		}
	}
	return ""
}

// BrowserRegister 使用rod浏览器自动化完成注册
type BrowserRegister struct {
	browser    *rod.Browser
	httpClient *HTTPClient
}

// Point 轨迹点
type Point struct {
	X, Y float64
}

func NewBrowserRegister() *BrowserRegister {
	return &BrowserRegister{
		httpClient: NewHTTPClient(),
	}
}

// 生成人类化的鼠标移动轨迹
// 公式: y = 14.7585 * x^0.5190 - 3.9874
func (br *BrowserRegister) generateHumanTrack(startX, startY, endX, endY float64) []Point {
	var movements []Point

	distance := endX - startX
	steps := 30 + rand.Intn(20)

	for i := 0; i <= steps; i++ {
		progress := float64(i) / float64(steps)
		// 缓动函数
		easedProgress := 1 - math.Pow(1-progress, 2)

		currentX := startX + distance*easedProgress
		// 使用给定公式计算Y偏移
		yOffset := 14.7585*math.Pow(currentX-startX, 0.5190) - 3.9874
		yOffset = yOffset*0.1 + float64(rand.Intn(5)-2)

		currentY := startY + yOffset

		movements = append(movements, Point{X: currentX, Y: currentY})
	}

	return movements
}

// SlideSlider 使用Gemini识别缺口位置并滑动
func (br *BrowserRegister) SlideSlider(page *rod.Page) error {
	maxRetries := 3

	for retry := 0; retry < maxRetries; retry++ {
		fmt.Printf("滑块验证尝试 %d/%d\n", retry+1, maxRetries)

		// 等待滑块加载
		slider, err := page.Timeout(5 * time.Second).Element("#aliyunCaptcha-sliding-slider")
		if err != nil || slider == nil {
			fmt.Println("未找到滑块，可能已验证成功")
			return nil
		}
		time.Sleep(500 * time.Millisecond)

		// 截取验证码图片 - 使用实际选择器
		imgEl, _ := page.Timeout(2 * time.Second).Element("div.puzzle, #aliyunCaptcha-img-box")

		var screenshot []byte
		if imgEl != nil {
			screenshot, err = imgEl.Screenshot(proto.PageCaptureScreenshotFormatPng, 100)
		}

		if screenshot == nil || err != nil {
			fmt.Println("截图失败，使用默认距离")
			// 使用默认距离直接滑动
			br.doSlideJS(page, 180+float64(rand.Intn(60)))
			time.Sleep(1500 * time.Millisecond)
			continue
		}

		// 使用Gemini识别缺口位置
		distance, err := br.analyzeWithGemini(screenshot)
		if err != nil {
			fmt.Printf("Gemini识别失败: %v，使用默认距离\n", err)
			distance = 180 + float64(rand.Intn(60))
		}
		fmt.Printf("识别到滑动距离: %.0f\n", distance)

		// Gemini返回滑动距离，加一点偏移补偿（模型往往少算10-15像素）
		adjustedDistance := distance + 17
		fmt.Printf("调整后距离: %.0f (原: %.0f)\n", adjustedDistance, distance)
		br.doSlideJS(page, adjustedDistance)

		time.Sleep(1500 * time.Millisecond)

		// 检查是否成功
		_, err = page.Timeout(1 * time.Second).Element("#aliyunCaptcha-sliding-slider")
		if err != nil {
			fmt.Println("验证成功!")
			return nil
		}

		// 刷新重试
		refreshBtn, _ := page.Timeout(500 * time.Millisecond).Element("#aliyunCaptcha-img-refresh")
		if refreshBtn != nil {
			refreshBtn.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(1 * time.Second)
		}
	}

	// 自动失败，等待手动
	fmt.Println("\n=== 自动验证失败，请手动完成 ===")
	for i := 0; i < 60; i++ {
		time.Sleep(1 * time.Second)
		_, err := page.Timeout(500 * time.Millisecond).Element("#aliyunCaptcha-sliding-slider")
		if err != nil {
			fmt.Println("检测到验证成功!")
			return nil
		}
	}
	return nil
}
func (br *BrowserRegister) analyzeWithGemini(screenshot []byte) (float64, error) {
	apiKey := ""
	apiURL := ""
	model := ""

	// 转base64
	imgBase64 := base64.StdEncoding.EncodeToString(screenshot)

	// OpenAI格式请求 - 提供完整信息让模型准确估算
	prompt := `这是一个滑块拼图验证码图片。
图片信息：
- 图片尺寸：300 x 200 像素
- 左侧有一个拼图滑块（约50x50像素），初始位置在 x=0
- 右侧背景中有一个缺口，滑块需要滑动到缺口位置才能验证通过
- 滑块的左边缘对齐图片左边缘

请分析图片中缺口的左边缘x坐标位置。这个x坐标就是滑块需要滑动的像素距离。
只返回一个整数，不要其他任何文字。`

	requestBody := fmt.Sprintf(`{
		"model": "%s",
		"messages": [{
			"role": "user",
			"content": [
				{"type": "text", "text": %q},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,%s"}}
			]
		}],
		"max_tokens": 50,
		"temperature": 0
	}`, model, prompt, imgBase64)

	client, err := internal.TLSHTTPClient(120 * time.Second)
	if err != nil {
		return 0, err
	}
	req, _ := fhttp.NewRequest("POST", apiURL, strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	internal.ApplyBrowserFingerprintHeaders(req.Header)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// 解析OpenAI格式响应
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("解析响应失败: %v, body: %s", err, string(body))
	}

	if len(result.Choices) > 0 {
		text := strings.TrimSpace(result.Choices[0].Message.Content)
		fmt.Printf("Gemini返回: %s\n", text)
		// 提取数字
		var distance float64
		fmt.Sscanf(text, "%f", &distance)
		if distance > 50 && distance < 280 {
			return distance, nil
		}
	}

	return 0, fmt.Errorf("无法解析Gemini响应: %s", string(body))
}

// doSlideJS 使用JS执行滑动
func (br *BrowserRegister) doSlideJS(page *rod.Page, distance float64) {
	fmt.Printf("JS滑动: %.0f 像素\n", distance)
	page.Eval(fmt.Sprintf(`() => {
		const slider = document.querySelector('#aliyunCaptcha-sliding-slider');
		if (!slider) return;
		
		const rect = slider.getBoundingClientRect();
		const startX = rect.left + rect.width / 2;
		const startY = rect.top + rect.height / 2;
		const endX = startX + %f;
		
		// mousedown
		slider.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, clientX: startX, clientY: startY}));
		
		// 逐步移动
		let x = startX;
		const move = () => {
			x += 8;
			if (x >= endX) x = endX;
			document.dispatchEvent(new MouseEvent('mousemove', {bubbles: true, cancelable: true, clientX: x, clientY: startY}));
			if (x < endX) {
				setTimeout(move, 15);
			} else {
				setTimeout(() => {
					document.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, clientX: x, clientY: startY}));
				}, 50);
			}
		};
		setTimeout(move, 30);
	}`, distance))
}

// doSlide 执行一次滑动
func (br *BrowserRegister) doSlide(page *rod.Page, startX, startY, distance float64) {
	page.Mouse.MustMoveTo(startX, startY)
	time.Sleep(50 * time.Millisecond)

	page.Mouse.MustDown(proto.InputMouseButtonLeft)
	time.Sleep(30 * time.Millisecond)

	// 人类化轨迹滑动
	endX := startX + distance
	track := br.generateHumanTrack(startX, startY, endX, startY)
	for _, point := range track {
		page.Mouse.MustMoveTo(point.X, point.Y)
		time.Sleep(time.Duration(10+rand.Intn(20)) * time.Millisecond)
	}

	time.Sleep(50 * time.Millisecond)
	page.Mouse.MustUp(proto.InputMouseButtonLeft)
}

// clickElement 安全点击元素
func (br *BrowserRegister) clickElement(page *rod.Page, selectors []string, desc string) bool {
	for _, sel := range selectors {
		el, err := page.Timeout(3 * time.Second).Element(sel)
		if err == nil && el != nil {
			if clickErr := el.Click(proto.InputMouseButtonLeft, 1); clickErr == nil {
				fmt.Printf("  %s: 已点击 (%s)\n", desc, sel)
				return true
			}
		}
	}
	return false
}

// clickElementByText 通过文本匹配点击元素
func (br *BrowserRegister) clickElementByText(page *rod.Page, tag, text, desc string) bool {
	el, err := page.Timeout(5*time.Second).ElementR(tag, text)
	if err == nil && el != nil {
		if clickErr := el.Click(proto.InputMouseButtonLeft, 1); clickErr == nil {
			fmt.Printf("  %s: 已点击\n", desc)
			return true
		}
	}
	fmt.Printf("  %s: 未找到\n", desc)
	return false
}

// inputText 安全输入文本
func (br *BrowserRegister) inputText(page *rod.Page, selectors []string, text, desc string) bool {
	for _, sel := range selectors {
		el, err := page.Timeout(2 * time.Second).Element(sel)
		if err == nil && el != nil {
			el.MustClick()
			el.MustSelectAllText().MustInput(text)
			fmt.Printf("  %s: 已输入\n", desc)
			return true
		}
	}
	fmt.Printf("  %s: 未找到输入框\n", desc)
	return false
}

func (br *BrowserRegister) Register(email, password string) (string, error) {
	// 启动浏览器
	path, found := launcher.LookPath()
	if !found {
		return "", fmt.Errorf("未找到系统浏览器")
	}
	fmt.Printf("使用浏览器: %s\n", path)

	l := launcher.New().Bin(path).Headless(false).
		Set("no-sandbox", "true").
		Set("disable-blink-features", "AutomationControlled").
		Set("disable-infobars", "true").
		Set("excludeSwitches", "enable-automation").
		Set("useAutomationExtension", "false")
	u, err := l.Launch()
	if err != nil {
		return "", fmt.Errorf("启动浏览器失败: %v", err)
	}

	br.browser = rod.New().ControlURL(u).MustConnect()
	defer br.browser.MustClose()

	page := br.browser.MustPage("https://chat.z.ai/auth")

	// 移除webdriver标记，规避自动化检测
	page.MustEval(`() => {
		Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
		Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]});
		Object.defineProperty(navigator, 'languages', {get: () => ['zh-CN', 'zh', 'en']});
		window.chrome = {runtime: {}};
	}`)

	br.clickElementByText(page, "button,span", "Email", "Continue with Email")
	br.clickElementByText(page, "button,a", "Sign up", "Sign up")
	br.inputText(page, []string{"input[placeholder*='Name']", "input[name='name']"}, email, "Name")
	br.inputText(page, []string{"input[type='email']", "input[name='email']"}, email, "Email")
	br.inputText(page, []string{"input[type='password']", "input[name='password']"}, password, "Password")
	// 点击验证按钮触发滑块弹窗
	if !br.clickElement(page, []string{"#aliyunCaptcha-captcha-text", "span[id*='captcha']"}, "验证按钮") {
		br.clickElementByText(page, "span,div", "verification", "验证按钮")
	}

	// 等待滑块弹窗完全加载
	fmt.Println("等待滑块弹窗加载...")
	time.Sleep(2 * time.Second)

	// 处理滑块验证
	br.SlideSlider(page)

	// 滑块验证完成后再点击Create Account
	time.Sleep(500 * time.Millisecond)
	br.clickElementByText(page, "button", "Create", "Create Account")

	// 等待提交完成
	fmt.Println("等待提交完成...")
	time.Sleep(5 * time.Second)

	// 关闭浏览器
	br.browser.MustClose()

	// 等待验证邮件
	fmt.Println("\n等待验证邮件...")
	verifyToken, err := br.httpClient.CheckEmail(email)
	if err != nil {
		return "", fmt.Errorf("获取验证邮件失败: %v", err)
	}
	fmt.Printf("获取到验证token: %s\n", verifyToken)

	// 通过HTTP请求完成注册
	token, err := br.httpClient.FinishSignup(email, password, verifyToken)
	if err != nil {
		return "", fmt.Errorf("完成注册失败: %v", err)
	}

	fmt.Printf("注册成功! Token: %s...\n", token[:50])
	return token, nil
}

// SaveToken 保存token到文件
func SaveToken(token string) error {
	dataDir := "data"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	tokenFile := filepath.Join(dataDir, "tokens.txt")
	f, err := os.OpenFile(tokenFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(token + "\n")
	return err
}

func main() {
	rand.Seed(time.Now().UnixNano())
	httpClient := NewHTTPClient()
	email, err := httpClient.GetTempEmail()
	if err != nil {
		fmt.Printf("获取临时邮箱失败: %v\n", err)
		os.Exit(1)
	}
	password := GeneratePassword()
	br := NewBrowserRegister()
	token, err := br.Register(email, password)
	if err != nil {
		fmt.Printf("注册失败: %v\n", err)
		os.Exit(1)
	}

	// 保存token
	fmt.Println("\n保存token...")
	if err := SaveToken(token); err != nil {
		fmt.Printf("保存token失败: %v\n", err)
	}

	fmt.Println("\n=== 注册成功 ===")
	fmt.Printf("邮箱: %s\n", email)
	fmt.Printf("密码: %s\n", password)
	fmt.Printf("Token: %s\n", token)
	fmt.Println("\nToken已保存到 data/tokens.txt")
}
