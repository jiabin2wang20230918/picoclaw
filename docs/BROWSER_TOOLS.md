# 浏览器工具使用指南

QuantClaw 现在支持完整的浏览器自动化功能，允许代理与网页进行交互。此功能通过配置文件启用，并使用 Chrome DevTools Protocol (CDP) 实现。

## 启用浏览器工具

在您的配置文件 (`config.json`) 中添加或修改 `tools.browser` 部分：

```json
{
  "tools": {
    "browser": {
      "enabled": true,
      "cdp_url": "http://localhost:9222"
    }
  }
}
```

## 可用的浏览器工具

以下浏览器工具现已完全实现：

- `browser_navigate`: 导航到指定 URL 并等待页面加载完成
- `browser_get_text`: 从特定元素或整个页面提取文本内容
- `browser_click`: 点击由 CSS 选择器标识的元素
- `browser_fill_input`: 填充输入字段或文本区域
- `browser_execute_script`: 在浏览器上下文中执行 JavaScript 代码
- `browser_screenshot`: 截取当前页面的屏幕截图

## 配置选项

- `enabled`: 是否启用浏览器工具（布尔值，默认为 false）
- `cdp_url`: Chrome DevTools Protocol 的 HTTP 端点 URL（例如：`http://localhost:9222`）

## 使用环境

要使完整功能生效，您需要：

1. 运行一个支持 Chrome DevTools Protocol 的浏览器（如 Chrome、Chromium）
2. 启动浏览器时启用远程调试：

   ```bash
   # 基本启动命令
   google-chrome --headless --remote-debugging-port=9222 --no-sandbox --disable-gpu --user-data-dir=/tmp/chrome_quantclaw_profile about:blank

   # 或者作为 systemd 服务运行
   sudo systemctl start chromium-quantclaw.service
   ```

3. 验证 CDP 服务是否运行：
   ```bash
   curl http://localhost:9222/json/version
   ```

## 功能详情

### browser_navigate
- 导航到指定 URL
- 等待页面完全加载
- 返回导航状态和框架信息

### browser_get_text
- 提取整个页面或特定元素的文本
- 支持 CSS 选择器定位元素
- 自动处理动态内容

### browser_click
- 通过 CSS 选择器定位并点击元素
- 自动滚动到目标元素
- 触发标准鼠标事件

### browser_fill_input
- 填充输入框或文本区域
- 触发 input 和 change 事件
- 支持各种表单控件类型

### browser_execute_script
- 在浏览器上下文执行 JavaScript
- 支持返回值获取
- 全访问 DOM 和 JavaScript API

### browser_screenshot
- 截取当前页面屏幕截图
- 输出 base64 编码的图像数据
- PNG 格式，80% 质量

## 安全注意事项

- 浏览器工具允许执行网页操作，可能涉及敏感数据
- 谨慎使用，并仅在信任的环境中启用
- 默认情况下，浏览器工具是禁用的
- 避免在生产环境中无限制地访问外部网站

## 环境变量

可以通过环境变量配置：

- `QUANTCLAW_TOOLS_BROWSER_ENABLED`: 设置为 "true" 以启用浏览器工具
- `QUANTCLAW_TOOLS_BROWSER_CDP_URL`: 设置 CDP URL

## 示例用法

启用后，代理可以使用浏览器工具执行以下操作：

- 访问和导航到各种网站
- 提取动态生成的内容
- 与表单和按钮交互
- 执行定制的 JavaScript 任务
- 获取网页视觉内容

此功能使得代理可以与现代动态网站进行交互，提供更丰富的互联网访问能力。