//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/sipeed/quantclaw/pkg/bus"
	"github.com/sipeed/quantclaw/pkg/config"
	"github.com/sipeed/quantclaw/pkg/logger"
	"github.com/sipeed/quantclaw/pkg/utils"
)

type FeishuChannel struct {
	*BaseChannel
	config   config.FeishuConfig
	client   *lark.Client
	wsClient *larkws.Client

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewFeishuChannel(cfg config.FeishuConfig, bus *bus.MessageBus) (*FeishuChannel, error) {
	base := NewBaseChannel("feishu", cfg, bus, cfg.AllowFrom)

	return &FeishuChannel{
		BaseChannel: base,
		config:      cfg,
		client:      lark.NewClient(cfg.AppID, cfg.AppSecret),
	}, nil
}

func (c *FeishuChannel) Start(ctx context.Context) error {
	if c.config.AppID == "" || c.config.AppSecret == "" {
		return fmt.Errorf("feishu app_id or app_secret is empty")
	}

	dispatcher := larkdispatcher.NewEventDispatcher(c.config.VerificationToken, c.config.EncryptKey).
		OnP2MessageReceiveV1(c.handleMessageReceive)

	runCtx, cancel := context.WithCancel(ctx)

	c.mu.Lock()
	c.cancel = cancel
	c.wsClient = larkws.NewClient(
		c.config.AppID,
		c.config.AppSecret,
		larkws.WithEventHandler(dispatcher),
	)
	wsClient := c.wsClient
	c.mu.Unlock()

	c.setRunning(true)
	logger.InfoC("feishu", "Feishu channel started (websocket mode)")

	go func() {
		if err := wsClient.Start(runCtx); err != nil {
			logger.ErrorCF("feishu", "Feishu websocket stopped with error", map[string]any{
				"error": err.Error(),
			})
		}
	}()

	return nil
}

func (c *FeishuChannel) Stop(ctx context.Context) error {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.wsClient = nil
	c.mu.Unlock()

	c.setRunning(false)
	logger.InfoC("feishu", "Feishu channel stopped")
	return nil
}

func (c *FeishuChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("feishu channel not running")
	}

	if msg.ChatID == "" {
		return fmt.Errorf("chat ID is empty")
	}

	// 检查是否有附件
	if len(msg.Attachments) > 0 {
		// 发送附件（暂不实现具体上传逻辑，可后续扩展）
		logger.WarnCF("feishu", "Attachments detected but not yet implemented for feishu", map[string]any{
			"chat_id":           msg.ChatID,
			"attachments_count": len(msg.Attachments),
		})

		// 发送消息内容，即使有附件也发送文本
		if msg.Content != "" {
			// 检查内容是否包含Markdown格式的特征
			if containsMarkdownElements(msg.Content) {
				return c.sendInteractiveCardMessage(ctx, msg)
			} else {
				return c.sendTextMessage(ctx, msg)
			}
		}
		return nil
	}

	// 检查内容是否包含Markdown格式的特征
	if containsMarkdownElements(msg.Content) {
		// 使用交互式卡片消息类型发送包含Markdown格式的内容
		return c.sendInteractiveCardMessage(ctx, msg)
	} else {
		// 使用文本消息类型发送普通文本
		return c.sendTextMessage(ctx, msg)
	}
}

// SendProgress sends progress updates to Feishu channel
func (c *FeishuChannel) SendProgress(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("feishu channel not running")
	}

	if msg.ChatID == "" {
		return fmt.Errorf("chat ID is empty for progress message")
	}

	// Prepare progress content - only show the content without percentage
	content := msg.Content

	// Create a new message with progress content
	progressMsg := bus.OutboundMessage{
		Channel:     msg.Channel,
		ChatID:      msg.ChatID,
		Content:     content,
		Attachments: msg.Attachments,
	}

	// Send as regular text message
	return c.sendTextMessage(ctx, progressMsg)
}

// sendTextMessage 发送普通文本消息
func (c *FeishuChannel) sendTextMessage(ctx context.Context, msg bus.OutboundMessage) error {
	payload, err := json.Marshal(map[string]string{"text": msg.Content})
	if err != nil {
		return fmt.Errorf("failed to marshal feishu content: %w", err)
	}

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(msg.ChatID).
			MsgType(larkim.MsgTypeText).
			Content(string(payload)).
			Uuid(fmt.Sprintf("quantclaw-%d", time.Now().UnixNano())).
			Build()).
		Build()

	resp, err := c.client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to send feishu text message: %w", err)
	}

	if !resp.Success() {
		return fmt.Errorf("feishu api error: code=%d msg=%s", resp.Code, resp.Msg)
	}

	logger.DebugCF("feishu", "Feishu text message sent", map[string]any{
		"chat_id": msg.ChatID,
	})

	return nil
}

// sendInteractiveCardMessage 发送交互式卡片消息以支持丰富的Markdown格式
func (c *FeishuChannel) sendInteractiveCardMessage(ctx context.Context, msg bus.OutboundMessage) error {
	// 构建交互式卡片消息
	card := c.buildInteractiveCard(msg.Content)

	cardJSON, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("failed to marshal feishu card content: %w", err)
	}

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(msg.ChatID).
			MsgType(larkim.MsgTypeInteractive).
			Content(string(cardJSON)).
			Uuid(fmt.Sprintf("quantclaw-%d", time.Now().UnixNano())).
			Build()).
		Build()

	resp, err := c.client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to send feishu interactive card message: %w", err)
	}

	if !resp.Success() {
		return fmt.Errorf("feishu api error: code=%d msg=%s", resp.Code, resp.Msg)
	}

	logger.DebugCF("feishu", "Feishu interactive card message sent", map[string]any{
		"chat_id": msg.ChatID,
	})

	return nil
}

// buildInteractiveCard 构建飞书交互式卡片
func (c *FeishuChannel) buildInteractiveCard(content string) map[string]interface{} {
	// 根据 Python 代码参考，使用卡片元素构建包含 Markdown 的交互式卡片
	elements := c.buildCardElements(content)

	card := map[string]interface{}{
		"config": map[string]interface{}{
			"wide_screen_mode": true,
		},
		"elements": elements,
	}

	return card
}

// buildCardElements 根据内容构建卡片元素，包括 Markdown 和表格
func (c *FeishuChannel) buildCardElements(content string) []map[string]interface{} {
	// 检查是否有表格格式
	if strings.Contains(content, "|") && strings.Contains(content, "-|") {
		return c.splitByHeadingsAndTables(content)
	}

	// 如果没有表格，直接返回 Markdown 元素
	return []map[string]interface{}{
		{
			"tag":     "markdown",
			"content": content,
		},
	}
}

// splitByHeadingsAndTables 根据标题和表格拆分内容
func (c *FeishuChannel) splitByHeadingsAndTables(content string) []map[string]interface{} {
	// 对于复杂内容，将 Markdown 作为整体发送
	// 如果以后需要更复杂的处理，可以在这里添加表格、标题等的解析
	elements := []map[string]interface{}{}

	// 检查是否有标题格式 (# ## ###)
	if strings.Contains(content, "\n# ") || strings.Contains(content, "\n## ") || strings.Contains(content, "\n### ") {
		// 如果包含标题，拆分处理
		elements = c.splitHeadings(content)
	} else {
		// 否则使用单个 markdown 元素
		elements = append(elements, map[string]interface{}{
			"tag":     "markdown",
			"content": content,
		})
	}

	return elements
}

// splitHeadings 拆分标题并将其转换为适当格式
func (c *FeishuChannel) splitHeadings(content string) []map[string]interface{} {
	elements := []map[string]interface{}{}

	lines := strings.Split(content, "\n")
	inCodeBlock := false
	var currentElementLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			currentElementLines = append(currentElementLines, line)
			continue
		}

		if inCodeBlock {
			currentElementLines = append(currentElementLines, line)
			continue
		}

		// 检查标题格式
		if strings.HasPrefix(line, "### ") {
			// 添加之前累积的元素
			if len(currentElementLines) > 0 {
				elementContent := strings.Join(currentElementLines, "\n")
				if strings.TrimSpace(elementContent) != "" {
					elements = append(elements, map[string]interface{}{
						"tag":     "markdown",
						"content": elementContent,
					})
				}
				currentElementLines = []string{}
			}

			// 添加标题元素
			title := strings.TrimSpace(line[4:])
			elements = append(elements, map[string]interface{}{
				"tag": "div",
				"text": map[string]interface{}{
					"tag":     "lark_md",
					"content": fmt.Sprintf("**%s**", title),
				},
			})
		} else if strings.HasPrefix(line, "## ") {
			// 添加之前累积的元素
			if len(currentElementLines) > 0 {
				elementContent := strings.Join(currentElementLines, "\n")
				if strings.TrimSpace(elementContent) != "" {
					elements = append(elements, map[string]interface{}{
						"tag":     "markdown",
						"content": elementContent,
					})
				}
				currentElementLines = []string{}
			}

			// 添加标题元素
			title := strings.TrimSpace(line[3:])
			elements = append(elements, map[string]interface{}{
				"tag": "div",
				"text": map[string]interface{}{
					"tag":     "lark_md",
					"content": fmt.Sprintf("**%s**", title),
				},
			})
		} else if strings.HasPrefix(line, "# ") {
			// 添加之前累积的元素
			if len(currentElementLines) > 0 {
				elementContent := strings.Join(currentElementLines, "\n")
				if strings.TrimSpace(elementContent) != "" {
					elements = append(elements, map[string]interface{}{
						"tag":     "markdown",
						"content": elementContent,
					})
				}
				currentElementLines = []string{}
			}

			// 添加标题元素
			title := strings.TrimSpace(line[2:])
			elements = append(elements, map[string]interface{}{
				"tag": "div",
				"text": map[string]interface{}{
					"tag":     "lark_md",
					"content": fmt.Sprintf("**%s**", title),
				},
			})
		} else {
			currentElementLines = append(currentElementLines, line)
		}
	}

	// 添加剩余的元素
	if len(currentElementLines) > 0 {
		elementContent := strings.Join(currentElementLines, "\n")
		if strings.TrimSpace(elementContent) != "" {
			elements = append(elements, map[string]interface{}{
				"tag":     "markdown",
				"content": elementContent,
			})
		}
	}

	return elements
}

// containsMarkdownElements 检查内容是否包含Markdown元素
func containsMarkdownElements(content string) bool {
	// 检查常见的Markdown标记
	markdownIndicators := []string{
		"**",   // 粗体
		"*",    // 斜体
		"__",   // 下划线
		"# ",   // 标题
		"## ",  // 标题
		"### ", // 标题
		"- ",   // 列表
		"* ",   // 列表
		"1. ",  // 数字列表
		"> ",   // 引用
		"`",    // 行内代码
		"```",  // 代码块
		"[",    // 链接开始
		"](",   // 链接结束
		"---",  // 分隔线
		"___",  // 分隔线
		"|",    // 表格
	}

	for _, indicator := range markdownIndicators {
		if strings.Contains(content, indicator) {
			return true
		}
	}

	return false
}

func (c *FeishuChannel) handleMessageReceive(_ context.Context, event *larkim.P2MessageReceiveV1) error {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}

	message := event.Event.Message
	sender := event.Event.Sender

	chatID := stringValue(message.ChatId)
	if chatID == "" {
		return nil
	}

	senderID := extractFeishuSenderID(sender)
	if senderID == "" {
		senderID = "unknown"
	}

	content := extractFeishuMessageContent(message)
	if content == "" {
		content = "[empty message]"
	}

	metadata := map[string]string{}
	if messageID := stringValue(message.MessageId); messageID != "" {
		metadata["message_id"] = messageID
	}
	if messageType := stringValue(message.MessageType); messageType != "" {
		metadata["message_type"] = messageType
	}
	if chatType := stringValue(message.ChatType); chatType != "" {
		metadata["chat_type"] = chatType
	}
	if sender != nil && sender.TenantKey != nil {
		metadata["tenant_key"] = *sender.TenantKey
	}

	chatType := stringValue(message.ChatType)
	if chatType == "p2p" {
		metadata["peer_kind"] = "direct"
		metadata["peer_id"] = senderID
	} else {
		metadata["peer_kind"] = "group"
		metadata["peer_id"] = chatID
	}

	logger.InfoCF("feishu", "Feishu message received", map[string]any{
		"sender_id": senderID,
		"chat_id":   chatID,
		"preview":   utils.Truncate(content, 80),
	})

	c.HandleMessage(senderID, chatID, content, nil, metadata)
	return nil
}

func extractFeishuSenderID(sender *larkim.EventSender) string {
	if sender == nil || sender.SenderId == nil {
		return ""
	}

	if sender.SenderId.UserId != nil && *sender.SenderId.UserId != "" {
		return *sender.SenderId.UserId
	}
	if sender.SenderId.OpenId != nil && *sender.SenderId.OpenId != "" {
		return *sender.SenderId.OpenId
	}
	if sender.SenderId.UnionId != nil && *sender.SenderId.UnionId != "" {
		return *sender.SenderId.UnionId
	}

	return ""
}

func extractFeishuMessageContent(message *larkim.EventMessage) string {
	if message == nil || message.Content == nil || *message.Content == "" {
		return ""
	}

	if message.MessageType != nil && *message.MessageType == larkim.MsgTypeText {
		var textPayload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(*message.Content), &textPayload); err == nil {
			return textPayload.Text
		}
	}

	return *message.Content
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
