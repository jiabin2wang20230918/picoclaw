package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/protocol/dom"
	"github.com/mafredri/cdp/protocol/page"
	"github.com/mafredri/cdp/protocol/runtime"
	"github.com/mafredri/cdp/rpcc"
)

// BrowserConfig holds configuration for browser tools
type BrowserConfig struct {
	Enabled bool   `json:"enabled"`
	CDPURL  string `json:"cdp_url,omitempty"`
}

// BrowserTool is the base struct for browser operations
type BrowserTool struct {
	config BrowserConfig
	conn   *rpcc.Conn
	client *cdp.Client
}

// NewBrowserTool creates a new browser tool instance
func NewBrowserTool(config BrowserConfig) *BrowserTool {
	return &BrowserTool{
		config: config,
	}
}

// connect establishes a connection to the Chrome DevTools Protocol
func (t *BrowserTool) connect() error {
	if t.conn != nil {
		// Already connected
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Connect to the devtools endpoint to get the websocket URL
	devt := devtool.New(t.config.CDPURL)

	// Try to get available targets
	targets, err := devt.List(ctx)
	if err != nil {
		// If we can't list targets, try to create a new one
		target, err := devt.Create(ctx)
		if err != nil {
			// As a last resort, try to connect to the default target
			// Some browsers may not allow creating new targets but will have default ones
			versionResp, verErr := devt.Version(ctx)
			if verErr != nil {
				return fmt.Errorf("failed to connect to browser: %w", err)
			}
			// Use the version response WebSocket URL if available
			t.conn, err = rpcc.DialContext(ctx, versionResp.WebSocketDebuggerURL)
			if err != nil {
				return fmt.Errorf("failed to dial CDP connection: %w", err)
			}
		} else {
			// Successfully created a new target
			t.conn, err = rpcc.DialContext(ctx, target.WebSocketDebuggerURL)
			if err != nil {
				return fmt.Errorf("failed to dial CDP connection: %w", err)
			}
		}
	} else {
		// Use the first available target
		if len(targets) > 0 {
			t.conn, err = rpcc.DialContext(ctx, targets[0].WebSocketDebuggerURL)
			if err != nil {
				return fmt.Errorf("failed to dial CDP connection: %w", err)
			}
		} else {
			// No targets available, create a new one
			target, err := devt.Create(ctx)
			if err != nil {
				return fmt.Errorf("no targets available and failed to create new target: %w", err)
			}
			t.conn, err = rpcc.DialContext(ctx, target.WebSocketDebuggerURL)
			if err != nil {
				return fmt.Errorf("failed to dial CDP connection: %w", err)
			}
		}
	}

	t.client = cdp.NewClient(t.conn)
	return nil
}

// disconnect closes the CDP connection
func (t *BrowserTool) disconnect() error {
	if t.conn != nil {
		err := t.conn.Close()
		t.conn = nil
		t.client = nil
		return err
	}
	return nil
}

// truncateString truncates a string to a maximum length with an ellipsis
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// parseHTML removes HTML tags to extract plain text
func parseHTML(html string) string {
	var result strings.Builder
	inTag := false

	for _, char := range html {
		if char == '<' {
			inTag = true
			continue
		} else if char == '>' {
			inTag = false
			continue
		}

		if !inTag {
			result.WriteRune(char)
		}
	}

	return result.String()
}

// BrowserNavigateTool implements the browser_navigate tool
type BrowserNavigateTool struct {
	browser *BrowserTool
}

// NewBrowserNavigateTool creates a new browser navigation tool
func NewBrowserNavigateTool(config BrowserConfig) *BrowserNavigateTool {
	return &BrowserNavigateTool{
		browser: NewBrowserTool(config),
	}
}

// Name returns the tool name
func (t *BrowserNavigateTool) Name() string {
	return "browser_navigate"
}

// Parameters returns the tool parameters schema
func (t *BrowserNavigateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to navigate to",
			},
		},
		"required": []string{"url"},
	}
}

// Description returns the tool description
func (t *BrowserNavigateTool) Description() string {
	return "Navigate to a URL in the browser"
}

// Execute performs the navigation operation
func (t *BrowserNavigateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return ErrorResult("missing or invalid 'url' argument")
	}

	err := t.browser.connect()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to connect to browser: %v", err))
	}
	defer t.browser.disconnect()

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Enable the page domain
	err = t.browser.client.Page.Enable(timeoutCtx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to enable page: %v", err))
	}

	// Navigate to the URL
	navArgs := page.NewNavigateArgs(url)
	frameID, err := t.browser.client.Page.Navigate(timeoutCtx, navArgs)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to navigate to URL: %v", err))
	}

	// Wait for page to load
	_, err = t.browser.client.Page.LoadEventFired(timeoutCtx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("page load event failed: %v", err))
	}

	resultStr := fmt.Sprintf(`{"success": true, "url": "%s", "frameID": "%s", "message": "Successfully navigated to %s"}`, url, frameID.FrameID, url)
	return UserResult(resultStr)
}

// BrowserGetTextTool implements the browser_get_text tool
type BrowserGetTextTool struct {
	browser *BrowserTool
}

// NewBrowserGetTextTool creates a new browser text extraction tool
func NewBrowserGetTextTool(config BrowserConfig) *BrowserGetTextTool {
	return &BrowserGetTextTool{
		browser: NewBrowserTool(config),
	}
}

// Name returns the tool name
func (t *BrowserGetTextTool) Name() string {
	return "browser_get_text"
}

// Parameters returns the tool parameters schema
func (t *BrowserGetTextTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector for element to extract text from (optional, if omitted gets all page text)",
			},
		},
	}
}

// Description returns the tool description
func (t *BrowserGetTextTool) Description() string {
	return "Extract text content from the current page or a specific element using CSS selector"
}

// Execute extracts text content from the page
func (t *BrowserGetTextTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	selector, ok := args["selector"].(string) // Optional - if empty, get all page text
	if !ok {
		selector = "" // default to empty if not provided
	}

	err := t.browser.connect()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to connect to browser: %v", err))
	}
	defer t.browser.disconnect()

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Enable DOM domain to access elements
	err = t.browser.client.DOM.Enable(timeoutCtx, dom.NewEnableArgs())
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to enable DOM: %v", err))
	}

	var textContent string
	if selector == "" {
		// Get all text content from the page
		evalArgs := runtime.NewEvaluateArgs("document.body.innerText")
		result, err := t.browser.client.Runtime.Evaluate(timeoutCtx, evalArgs)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to evaluate page text: %v", err))
		}
		// Extract the value from the result
		textContent = fmt.Sprintf("%v", result.Result.Value)
	} else {
		// Get text from a specific element
		jsCode := fmt.Sprintf(`(() => {
			const element = document.querySelector('%s');
			if (!element) return 'Element not found';
			return element.innerText || element.textContent;
		})()`, strings.ReplaceAll(selector, "'", "\\'"))

		evalArgs := runtime.NewEvaluateArgs(jsCode)
		result, err := t.browser.client.Runtime.Evaluate(timeoutCtx, evalArgs)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to evaluate element text: %v", err))
		}
		// Extract the value from the result
		textContent = fmt.Sprintf("%v", result.Result.Value)
	}

	truncatedText := truncateString(textContent, 4000) // Limit response size

	resultStr := fmt.Sprintf(`{"success": true, "text": "%s", "truncated": %t, "selector": "%s", "length": %d}`,
		strings.ReplaceAll(truncatedText, "\"", "\\\""), len(truncatedText) < len(textContent), selector, len(textContent))

	return UserResult(resultStr)
}

// BrowserClickTool implements the browser_click tool
type BrowserClickTool struct {
	browser *BrowserTool
}

// NewBrowserClickTool creates a new browser click tool
func NewBrowserClickTool(config BrowserConfig) *BrowserClickTool {
	return &BrowserClickTool{
		browser: NewBrowserTool(config),
	}
}

// Name returns the tool name
func (t *BrowserClickTool) Name() string {
	return "browser_click"
}

// Parameters returns the tool parameters schema
func (t *BrowserClickTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector for the element to click",
			},
		},
		"required": []string{"selector"},
	}
}

// Description returns the tool description
func (t *BrowserClickTool) Description() string {
	return "Click an element on the page identified by CSS selector"
}

// Execute clicks an element on the page
func (t *BrowserClickTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	selector, ok := args["selector"].(string)
	if !ok || selector == "" {
		return ErrorResult("missing or invalid 'selector' argument")
	}

	err := t.browser.connect()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to connect to browser: %v", err))
	}
	defer t.browser.disconnect()

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Enable DOM and Runtime domains
	err = t.browser.client.DOM.Enable(timeoutCtx, dom.NewEnableArgs())
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to enable DOM: %v", err))
	}

	// Evaluate and click the element
	jsCode := fmt.Sprintf(`(() => {
		const element = document.querySelector('%s');
		if (!element) return { success: false, message: 'Element not found' };

		// Scroll element into view
		element.scrollIntoView({ behavior: 'smooth', block: 'center' });

		// Dispatch click event
		const clickEvent = new MouseEvent('click', {
			view: window,
			bubbles: true,
			cancelable: true,
			buttons: 1
		});
		const clicked = element.dispatchEvent(clickEvent);

		return {
			success: true,
			message: 'Element clicked successfully',
			tagName: element.tagName,
			id: element.id,
			className: element.className
		};
	})()`, strings.ReplaceAll(selector, "'", "\\'"))

	evalArgs := runtime.NewEvaluateArgs(jsCode)
	result, err := t.browser.client.Runtime.Evaluate(timeoutCtx, evalArgs)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to evaluate click action: %v", err))
	}

	resultStr := fmt.Sprintf(`{"result": %s}`, fmt.Sprintf("%v", result.Result.Value))
	return UserResult(resultStr)
}

// BrowserFillInputTool implements the browser_fill_input tool
type BrowserFillInputTool struct {
	browser *BrowserTool
}

// NewBrowserFillInputTool creates a new browser fill input tool
func NewBrowserFillInputTool(config BrowserConfig) *BrowserFillInputTool {
	return &BrowserFillInputTool{
		browser: NewBrowserTool(config),
	}
}

// Name returns the tool name
func (t *BrowserFillInputTool) Name() string {
	return "browser_fill_input"
}

// Parameters returns the tool parameters schema
func (t *BrowserFillInputTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector for the input field to fill",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Text to fill into the input field",
			},
		},
		"required": []string{"selector", "text"},
	}
}

// Description returns the tool description
func (t *BrowserFillInputTool) Description() string {
	return "Fill an input field or textarea with text"
}

// Execute fills an input field with the provided text
func (t *BrowserFillInputTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	selector, ok := args["selector"].(string)
	if !ok || selector == "" {
		return ErrorResult("missing or invalid 'selector' argument")
	}

	text, ok := args["text"].(string)
	if !ok || text == "" {
		return ErrorResult("missing or invalid 'text' argument")
	}

	err := t.browser.connect()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to connect to browser: %v", err))
	}
	defer t.browser.disconnect()

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Enable DOM and Runtime domains
	err = t.browser.client.DOM.Enable(timeoutCtx, dom.NewEnableArgs())
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to enable DOM: %v", err))
	}

	// Fill the input field
	jsCode := fmt.Sprintf(`(() => {
		const element = document.querySelector('%s');
		if (!element) return { success: false, message: 'Element not found' };

		// Fill the input field
		element.value = '%s';

		// Trigger input and change events
		element.dispatchEvent(new Event('input', { bubbles: true }));
		element.dispatchEvent(new Event('change', { bubbles: true }));

		return {
			success: true,
			message: 'Input filled successfully',
			tagName: element.tagName,
			id: element.id,
			value: element.value
		};
	})()`, strings.ReplaceAll(selector, "'", "\\'"), strings.ReplaceAll(text, "'", "\\'"))

	evalArgs := runtime.NewEvaluateArgs(jsCode)
	result, err := t.browser.client.Runtime.Evaluate(timeoutCtx, evalArgs)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to evaluate fill action: %v", err))
	}

	resultStr := fmt.Sprintf(`{"result": %s}`, fmt.Sprintf("%v", result.Result.Value))
	return UserResult(resultStr)
}

// BrowserExecuteScriptTool implements the browser_execute_script tool
type BrowserExecuteScriptTool struct {
	browser *BrowserTool
}

// NewBrowserExecuteScriptTool creates a new browser script execution tool
func NewBrowserExecuteScriptTool(config BrowserConfig) *BrowserExecuteScriptTool {
	return &BrowserExecuteScriptTool{
		browser: NewBrowserTool(config),
	}
}

// Name returns the tool name
func (t *BrowserExecuteScriptTool) Name() string {
	return "browser_execute_script"
}

// Parameters returns the tool parameters schema
func (t *BrowserExecuteScriptTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"script": map[string]any{
				"type":        "string",
				"description": "JavaScript code to execute in the browser context",
			},
		},
		"required": []string{"script"},
	}
}

// Description returns the tool description
func (t *BrowserExecuteScriptTool) Description() string {
	return "Execute JavaScript in the browser context"
}

// Execute runs the provided JavaScript code
func (t *BrowserExecuteScriptTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	script, ok := args["script"].(string)
	if !ok || script == "" {
		return ErrorResult("missing or invalid 'script' argument")
	}

	err := t.browser.connect()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to connect to browser: %v", err))
	}
	defer t.browser.disconnect()

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Execute the script
	evalArgs := runtime.NewEvaluateArgs(script)
	result, err := t.browser.client.Runtime.Evaluate(timeoutCtx, evalArgs)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to execute script: %v", err))
	}

	resultStr := fmt.Sprintf(`{"success": true, "result": %s, "type": "%s"}`, fmt.Sprintf("%v", result.Result.Value), result.Result.Type)
	return UserResult(resultStr)
}

// BrowserScreenshotTool implements the browser_screenshot tool
type BrowserScreenshotTool struct {
	browser *BrowserTool
}

// NewBrowserScreenshotTool creates a new browser screenshot tool
func NewBrowserScreenshotTool(config BrowserConfig) *BrowserScreenshotTool {
	return &BrowserScreenshotTool{
		browser: NewBrowserTool(config),
	}
}

// Name returns the tool name
func (t *BrowserScreenshotTool) Name() string {
	return "browser_screenshot"
}

// Parameters returns the tool parameters schema
func (t *BrowserScreenshotTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{}, // No required parameters for taking a screenshot of current page
	}
}

// Description returns the tool description
func (t *BrowserScreenshotTool) Description() string {
	return "Take a screenshot of the current page"
}

// Execute captures a screenshot of the page
func (t *BrowserScreenshotTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	err := t.browser.connect()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to connect to browser: %v", err))
	}
	defer t.browser.disconnect()

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Enable Page domain
	err = t.browser.client.Page.Enable(timeoutCtx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to enable page: %v", err))
	}

	// Capture screenshot
	captureArgs := page.NewCaptureScreenshotArgs()
	format := "png"
	captureArgs.Format = &format
	quality := 80
	captureArgs.Quality = &quality

	screenshot, err := t.browser.client.Page.CaptureScreenshot(timeoutCtx, captureArgs)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to capture screenshot: %v", err))
	}

	// Encode the screenshot as base64
	imgData := base64.StdEncoding.EncodeToString([]byte(screenshot.Data))

	resultStr := fmt.Sprintf(`{"success": true, "data": "%s", "format": "png", "size": %d}`, imgData, len(imgData))
	return UserResult(resultStr)
}