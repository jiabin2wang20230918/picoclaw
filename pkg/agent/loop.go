// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/agent/memory"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/routing"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/state"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type AgentLoop struct {
	bus            *bus.MessageBus
	cfg            *config.Config
	registry       *AgentRegistry
	state          *state.Manager
	memoryManager  *memory.MemoryManager  // 新增：内存管理器
	running        atomic.Bool
	summarizing    sync.Map
	fallback       *providers.FallbackChain
	channelManager *channels.Manager
	activeSessions sync.Map // tracks sessions currently being processed

	// Modular architecture components placeholder for future Beehive integration
	useModular     bool // Feature flag to control which system is used
	toolHandler    *ToolHandler // 新增：工具处理器
	sessionManager *SessionManager // 新增：会话管理器
	messageBuilder *MessageBuilder // 新增：消息构建器
}

// ProgressCallback is a function type for reporting progress during agent execution
type ProgressCallback func(progress float64, message string, metadata map[string]interface{}) error

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey      string // Session identifier for history/context
	Channel         string // Target channel for tool execution
	ChatID          string // Target chat ID for tool execution
	UserMessage     string // User message content (may include prefix)
	DefaultResponse string // Response when LLM returns empty
	EnableSummary   bool   // Whether to trigger summarization
	NoHistory       bool   // If true, don't load session history (for heartbeat)
}

// progressCallbackFunc wraps the progress callback to publish progress messages via the bus
func (al *AgentLoop) progressCallbackFunc(channel, chatID string) ProgressCallback {
	return func(progress float64, message string, metadata map[string]interface{}) error {
		if al.cfg.Agents.Defaults.SendProgress { // Only send if progress sending is enabled
			// Convert metadata map[string]interface{} to map[string]string
			stringMetadata := make(map[string]string)
			for k, v := range metadata {
				if v != nil {
					switch val := v.(type) {
					case string:
						stringMetadata[k] = val
					case int:
						stringMetadata[k] = fmt.Sprintf("%d", val)
					case float64:
						stringMetadata[k] = fmt.Sprintf("%g", val)
					case bool:
						stringMetadata[k] = fmt.Sprintf("%t", val)
					default:
						stringMetadata[k] = fmt.Sprintf("%v", val)
					}
				}
			}

			progressMsg := bus.OutboundMessage{
				Channel:     channel,
				ChatID:      chatID,
				Content:     message,
				MessageType: bus.MessageTypeProgress,
				Progress:    &progress,
				Metadata:    stringMetadata,
			}
			logger.DebugCF("agent", "Publishing progress message", map[string]any{
				"channel": channel,
				"chat_id": chatID,
				"content": message,
				"progress": progress,
				"metadata": stringMetadata,
			})
			al.bus.PublishOutbound(progressMsg)
		} else {
			logger.DebugCF("agent", "Progress sending disabled, skipping progress message", map[string]any{
				"channel": channel,
				"chat_id": chatID,
				"content": message,
				"progress": progress,
			})
		}
		return nil
	}
}

func NewAgentLoop(cfg *config.Config, msgBus *bus.MessageBus, provider providers.LLMProvider) *AgentLoop {
	registry := NewAgentRegistry(cfg, provider)

	// Register shared tools to all agents
	registerSharedTools(cfg, msgBus, registry, provider)

	// Set up shared fallback chain
	cooldown := providers.NewCooldownTracker()
	fallbackChain := providers.NewFallbackChain(cooldown)

	// Create state manager using default agent's workspace for channel recording
	defaultAgent := registry.GetDefaultAgent()
	var stateManager *state.Manager
	if defaultAgent != nil {
		stateManager = state.NewManager(defaultAgent.Workspace)
	}

	// 新增：初始化内存管理器
	var memoryManager *memory.MemoryManager
	if cfg.Memory.Enabled {
		var err error
		memoryManager, err = memory.NewMemoryManager(cfg.WorkspacePath())
		if err != nil {
			logger.ErrorCF("memory", "Failed to initialize memory manager", map[string]any{"error": err})
			// 可选：降级到禁用状态而不是失败
			memoryManager = nil
		}
	}

	// Default to original system, can be overridden via config/env
	useModular := false // Use feature flag to enable modular system

	// 创建ToolHandler
	var toolHandler *ToolHandler
	if defaultAgent != nil {
		toolHandler = NewToolHandler(cfg, defaultAgent.Tools, msgBus)
	} else {
		// 如果没有默认代理，则创建一个基础的toolHandler，或者从registry获取其他可用代理
		// 临时使用nil，稍后再进行处理
		toolHandler = nil
	}

	// 创建分层组件
	var sessionManager *SessionManager
	var messageBuilder *MessageBuilder
	if defaultAgent != nil {
		sessionManager = NewSessionManager(defaultAgent)
		messageBuilder = NewMessageBuilder(sessionManager)
	} else {
		sessionManager = nil
		messageBuilder = nil
	}

	return &AgentLoop{
		bus:            msgBus,
		cfg:            cfg,
		registry:       registry,
		state:          stateManager,
		memoryManager:  memoryManager,  // 添加到实例
		summarizing:    sync.Map{},
		fallback:       fallbackChain,
		// Initialize modular architecture support but default to disabled
		useModular:     useModular,
		toolHandler:    toolHandler, // 添加ToolHandler
		sessionManager: sessionManager, // 添加SessionManager
		messageBuilder: messageBuilder, // 添加MessageBuilder
	}
}

// registerSharedTools registers tools that are shared across all agents (web, message, spawn).
func registerSharedTools(
	cfg *config.Config,
	msgBus *bus.MessageBus,
	registry *AgentRegistry,
	provider providers.LLMProvider,
) {
	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok {
			continue
		}

		// Web tools
		if searchTool := tools.NewWebSearchTool(tools.WebSearchToolOptions{
			BraveAPIKey:          cfg.Tools.Web.Brave.APIKey,
			BraveMaxResults:      cfg.Tools.Web.Brave.MaxResults,
			BraveEnabled:         cfg.Tools.Web.Brave.Enabled,
			TavilyAPIKey:         cfg.Tools.Web.Tavily.APIKey,
			TavilyBaseURL:        cfg.Tools.Web.Tavily.BaseURL,
			TavilyMaxResults:     cfg.Tools.Web.Tavily.MaxResults,
			TavilyEnabled:        cfg.Tools.Web.Tavily.Enabled,
			DuckDuckGoMaxResults: cfg.Tools.Web.DuckDuckGo.MaxResults,
			DuckDuckGoEnabled:    cfg.Tools.Web.DuckDuckGo.Enabled,
			PerplexityAPIKey:     cfg.Tools.Web.Perplexity.APIKey,
			PerplexityMaxResults: cfg.Tools.Web.Perplexity.MaxResults,
			PerplexityEnabled:    cfg.Tools.Web.Perplexity.Enabled,
		}); searchTool != nil {
			agent.Tools.Register(searchTool)
		}
		agent.Tools.Register(tools.NewWebFetchTool(50000))

		// Hardware tools (I2C, SPI) - Linux only, returns error on other platforms
		agent.Tools.Register(tools.NewI2CTool())
		agent.Tools.Register(tools.NewSPITool())

		// Message tool
		messageTool := tools.NewMessageTool()
		messageTool.SetSendCallback(func(channel, chatID, content string) error {
			msgBus.PublishOutbound(bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Content: content,
			})
			return nil
		})
		agent.Tools.Register(messageTool)

		// Skill discovery and installation tools
		registryMgr := skills.NewRegistryManagerFromConfig(skills.RegistryConfig{
			MaxConcurrentSearches: cfg.Tools.Skills.MaxConcurrentSearches,
			ClawHub:               skills.ClawHubConfig(cfg.Tools.Skills.Registries.ClawHub),
		})
		searchCache := skills.NewSearchCache(
			cfg.Tools.Skills.SearchCache.MaxSize,
			time.Duration(cfg.Tools.Skills.SearchCache.TTLSeconds)*time.Second,
		)
		agent.Tools.Register(tools.NewFindSkillsTool(registryMgr, searchCache))
		agent.Tools.Register(tools.NewInstallSkillTool(registryMgr, agent.Workspace))

		// Spawn tool with allowlist checker
		subagentManager := tools.NewSubagentManager(provider, agent.Model, agent.Workspace, msgBus)
		subagentManager.SetLLMOptions(agent.MaxTokens, agent.Temperature)

		// 设置默认资源限制
		maxConcurrent := 5
		timeout := 10 * time.Minute
		subagentManager.SetResourceLimits(maxConcurrent, timeout)

		spawnTool := tools.NewSpawnTool(subagentManager)
		currentAgentID := agentID
		spawnTool.SetAllowlistChecker(func(targetAgentID string) bool {
			return registry.CanSpawnSubagent(currentAgentID, targetAgentID)
		})
		agent.Tools.Register(spawnTool)

		// Update context builder with the complete tools registry
		agent.ContextBuilder.SetToolsRegistry(agent.Tools)

		// Register the skill loader tool to enable on-demand skill loading
		skillLoaderTool := &SkillLoaderTool{skillsLoader: agent.ContextBuilder.skillsLoader}
		agent.Tools.Register(skillLoaderTool)
	}
}

func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)

	for al.running.Load() {
		select {
		case <-ctx.Done():
			return nil
		default:
			msg, ok := al.bus.ConsumeInbound(ctx)
			if !ok {
				continue
			}

			// Determine the session key for this message
			sessionKey := al.determineSessionKey(msg)

			// Check if this session is currently being processed (auto-detect interruption)
			if _, isActive := al.activeSessions.Load(sessionKey); isActive {
				// Convert to steering message instead of queuing
				al.bus.PublishSteering(bus.SteeringMessage{
					Channel:    msg.Channel,
					ChatID:     msg.ChatID,
					Content:    msg.Content,
					SessionKey: sessionKey,
					Timestamp:  time.Now().Unix(),
				})
				logger.InfoCF("agent", "Steering message published (auto-detect)",
					map[string]any{
						"session_key": sessionKey,
						"channel":     msg.Channel,
					})
				continue
			}

			// Mark session as active
			al.activeSessions.Store(sessionKey, true)

			response, err := al.processMessage(ctx, msg)

			// Mark session as inactive
			al.activeSessions.Delete(sessionKey)

			if err != nil {
				response = fmt.Sprintf("Error processing message: %v", err)
			}

			if response != "" {
				// Check if the message tool already sent a response during this round.
				// If so, skip publishing to avoid duplicate messages to the user.
				// Use default agent's tools to check (message tool is shared).
				alreadySent := false
				defaultAgent := al.registry.GetDefaultAgent()
				if defaultAgent != nil {
					if tool, ok := defaultAgent.Tools.Get("message"); ok {
						if mt, ok := tool.(*tools.MessageTool); ok {
							alreadySent = mt.HasSentInRound()
						}
					}
				}

				// For channels like Feishu, we want to ensure that the final synthesized response
				// is always delivered to the user, even if intermediate tool results were sent.
				// The alreadySent flag might prevent the final comprehensive answer from reaching the user.
				// So we'll send the final response on channels where this is important.
				channelType := msg.Channel
				if channelType == "feishu" {
					// For Feishu, always send the final response to ensure user gets the complete answer
					al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: msg.Channel,
						ChatID:  msg.ChatID,
						Content: response,
					})
				} else {
					// For other channels, respect the alreadySent flag to avoid duplication
					if !alreadySent {
						al.bus.PublishOutbound(bus.OutboundMessage{
							Channel: msg.Channel,
							ChatID:  msg.ChatID,
							Content: response,
						})
					}
				}
			}
		}
	}

	return nil
}

func (al *AgentLoop) Stop() {
	al.running.Store(false)
}

// determineSessionKey determines the session key for a given message.
// This mirrors the routing logic in processMessage to identify active sessions.
func (al *AgentLoop) determineSessionKey(msg bus.InboundMessage) string {
	// Honor pre-set agent-scoped keys (for ProcessDirect/cron)
	if msg.SessionKey != "" && strings.HasPrefix(msg.SessionKey, "agent:") {
		return msg.SessionKey
	}

	// Route to determine agent and session key
	route := al.registry.ResolveRoute(routing.RouteInput{
		Channel:    msg.Channel,
		AccountID:  msg.Metadata["account_id"],
		Peer:       extractPeer(msg),
		ParentPeer: extractParentPeer(msg),
		GuildID:    msg.Metadata["guild_id"],
		TeamID:     msg.Metadata["team_id"],
	})

	return route.SessionKey
}

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	for _, agentID := range al.registry.ListAgentIDs() {
		if agent, ok := al.registry.GetAgent(agentID); ok {
			agent.Tools.Register(tool)
		}
	}
}

func (al *AgentLoop) SetChannelManager(cm *channels.Manager) {
	al.channelManager = cm
}

// RecordLastChannel records the last active channel for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChannel(channel string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChannel(channel)
}

// RecordLastChatID records the last active chat ID for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChatID(chatID string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChatID(chatID)
}

func (al *AgentLoop) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return al.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

func (al *AgentLoop) ProcessDirectWithChannel(
	ctx context.Context,
	content, sessionKey, channel, chatID string,
) (string, error) {
	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "cron",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	return al.processMessage(ctx, msg)
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
func (al *AgentLoop) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	agent := al.registry.GetDefaultAgent()
	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      "heartbeat",
		Channel:         channel,
		ChatID:          chatID,
		UserMessage:     content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   false,
		NoHistory:       true, // Don't load session history for heartbeat
	})
}

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(msg.Content, "Error:") || strings.Contains(msg.Content, "error") {
		logContent = msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(msg.Content, 80)
	}
	logger.InfoCF("agent", fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, logContent),
		map[string]any{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.SenderID,
			"session_key": msg.SessionKey,
		})

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return al.processSystemMessage(ctx, msg)
	}

	// Check for commands
	if response, handled := al.handleCommand(ctx, msg); handled {
		return response, nil
	}

	// Route to determine agent and session key
	route := al.registry.ResolveRoute(routing.RouteInput{
		Channel:    msg.Channel,
		AccountID:  msg.Metadata["account_id"],
		Peer:       extractPeer(msg),
		ParentPeer: extractParentPeer(msg),
		GuildID:    msg.Metadata["guild_id"],
		TeamID:     msg.Metadata["team_id"],
	})

	agent, ok := al.registry.GetAgent(route.AgentID)
	if !ok {
		agent = al.registry.GetDefaultAgent()
	}

	// Send initial progress feedback to let user know their request is being processed
	if al.cfg.Agents.Defaults.SendProgress && al.cfg.Agents.Defaults.SendToolHints { // Require both flags to show initial progress
		initialProgressMsg := bus.OutboundMessage{
			Channel:     msg.Channel,
			ChatID:      msg.ChatID,
			Content:     "Received your request, starting to process...",
			MessageType: bus.MessageTypeProgress,
			Metadata: map[string]string{
				"phase": "initial_processing",
			},
		}
		logger.DebugCF("agent", "Publishing initial progress message", map[string]any{
			"channel": msg.Channel,
			"chat_id": msg.ChatID,
			"content": initialProgressMsg.Content,
		})
		al.bus.PublishOutbound(initialProgressMsg)
	}

	// Use routed session key, but honor pre-set agent-scoped keys (for ProcessDirect/cron)
	sessionKey := route.SessionKey
	if msg.SessionKey != "" && strings.HasPrefix(msg.SessionKey, "agent:") {
		sessionKey = msg.SessionKey
	}

	logger.InfoCF("agent", "Routed message",
		map[string]any{
			"agent_id":    agent.ID,
			"session_key": sessionKey,
			"matched_by":  route.MatchedBy,
		})

	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		UserMessage:     msg.Content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		NoHistory:       false,
	})
}

func (al *AgentLoop) processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]any{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel, originChatID string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
		originChatID = msg.ChatID[idx+1:]
	} else {
		originChannel = "cli"
		originChatID = msg.ChatID
	}

	// Attempt to parse as structured JSON result first
	content := msg.Content
	var parsedResult map[string]interface{}
	if err := json.Unmarshal([]byte(content), &parsedResult); err == nil {
		// This is a structured result from subagent
		status, ok := parsedResult["status"].(string)
		if !ok {
			status = "unknown"
		}

		var resultContent string
		if data, exists := parsedResult["data"]; exists {
			switch v := data.(type) {
			case string:
				resultContent = v
			case map[string]interface{}:
				// Convert structured data back to string if needed
				if bytes, err := json.Marshal(v); err == nil {
					resultContent = string(bytes)
				} else {
					resultContent = fmt.Sprintf("%v", v)
				}
			default:
				resultContent = fmt.Sprintf("%v", v)
			}
		} else {
			resultContent = "No data in result"
		}

		// Format a user-friendly message based on status
		switch status {
		case "completed":
			content = fmt.Sprintf("[Subagent completed] %s", resultContent)
		case "failed":
			errorMsg, hasError := parsedResult["error"].(string)
			if hasError {
				content = fmt.Sprintf("[Subagent failed] Error: %s", errorMsg)
			} else {
				content = fmt.Sprintf("[Subagent failed] %s", resultContent)
			}
		case "cancelled":
			content = fmt.Sprintf("[Subagent cancelled] %s", resultContent)
		default:
			content = fmt.Sprintf("[Subagent %s] %s", status, resultContent)
		}
	} else {
		// This is legacy string format, extract content as before
		if idx := strings.Index(content, "Result:\n"); idx >= 0 {
			content = content[idx+8:] // Extract just the result part
		}
	}

	// Skip internal channels - only log, don't send to user
	if constants.IsInternalChannel(originChannel) {
		logger.InfoCF("agent", "Subagent completed (internal channel)",
			map[string]any{
				"sender_id":   msg.SenderID,
				"content_len": len(content),
				"channel":     originChannel,
			})
		return "", nil
	}

	// Use default agent for system messages
	agent := al.registry.GetDefaultAgent()

	// Use the origin session for context
	sessionKey := routing.BuildAgentMainSessionKey(agent.ID)

	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         originChannel,
		ChatID:          originChatID,
		UserMessage:     fmt.Sprintf("[System: %s] %s", msg.SenderID, content),
		DefaultResponse: "Background task completed.",
		EnableSummary:   false,
		NoHistory:       false,
	})
}

// runAgentLoop is the core message processing logic.
func (al *AgentLoop) runAgentLoop(ctx context.Context, agent *AgentInstance, opts processOptions) (string, error) {
	// 0. Record last channel for heartbeat notifications (skip internal channels)
	if opts.Channel != "" && opts.ChatID != "" {
		// Don't record internal channels (cli, system, subagent)
		if !constants.IsInternalChannel(opts.Channel) {
			channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
			if err := al.RecordLastChannel(channelKey); err != nil {
				logger.WarnCF("agent", "Failed to record last channel", map[string]any{"error": err.Error()})
			}
		}
	}

	// 1. Update tool contexts
	al.updateToolContexts(agent, opts.Channel, opts.ChatID)

	// 2. Build messages (skip history for heartbeat)
	var history []providers.Message
	var summary string
	if !opts.NoHistory {
		if al.sessionManager != nil {
			history = al.sessionManager.GetHistory(opts.SessionKey)
			summary = al.sessionManager.GetSummary(opts.SessionKey)
		} else {
			history = agent.Sessions.GetHistory(opts.SessionKey)
			summary = agent.Sessions.GetSummary(opts.SessionKey)
		}
	}

	// 新增：从记忆系统获取相关上下文
	var memoryContext string
	if al.memoryManager != nil {
		memCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = memCtx // 显式使用ctx变量避免未使用警告
		// 使用Search方法获取相关记忆上下文
		searchResults, err := al.memoryManager.Search(opts.UserMessage, 5) // 获取最多5个相关项
		if err != nil {
			logger.WarnCF("memory", "Failed to search memory context", map[string]any{"error": err})
		} else if len(searchResults) > 0 {
			// 将搜索结果格式化为上下文字符串
			var contextBuilder strings.Builder
			contextBuilder.WriteString("## Relevant Past Memories\n")

			for i, result := range searchResults {
				if i >= 3 { // 限制最多3个最相关项目
					break
				}

				// 确保内容不过长
				content := result.Content
				if len(content) > 500 {
					content = content[:500] + "..."
				}

				contextBuilder.WriteString(fmt.Sprintf("### Memory %d: %s\n", i+1, result.Key))
				contextBuilder.WriteString(content)
				contextBuilder.WriteString("\n\n")
			}

			memoryContext = contextBuilder.String()
		}
		cancel()
	}

	// 构建消息
	var messages []providers.Message
	if al.messageBuilder != nil {
		// 使用新组件构建消息（如果可用）并传递实际的配置
		// 需要转换配置类型
		compactionConfig := CompactionConfig{
			ReserveTokens:       agent.CompactionConfig.ReserveTokens,
			KeepRecentTokens:    agent.CompactionConfig.KeepRecentTokens,
			ReserveTokensFloor:  agent.CompactionConfig.ReserveTokensFloor,
			MemoryFlushEnabled:  agent.CompactionConfig.MemoryFlush.Enabled,
			SoftThresholdTokens: agent.CompactionConfig.MemoryFlush.SoftThresholdTokens,
		}
		messages = al.messageBuilder.BuildMessagesWithConfig(
			history,
			summary,
			opts.UserMessage,
			memoryContext,
			"", // media参数
			opts.Channel,
			opts.ChatID,
			compactionConfig, // 使用转换后的配置
		)
	} else {
		// 后备实现
		messages = agent.ContextBuilder.BuildMessages(
			history,
			summary,
			opts.UserMessage,
			memoryContext,  // 新增参数：记忆上下文
			nil,            // media参数
			opts.Channel,
			opts.ChatID,
		)
	}

	// 3. Save user message to session
	if al.sessionManager != nil {
		al.sessionManager.AddUserMessage(opts.SessionKey, opts.UserMessage)
	} else {
		agent.Sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)
	}

	// 4. Run LLM iteration loop
	progressCallback := al.progressCallbackFunc(opts.Channel, opts.ChatID)
	finalContent, iteration, err := al.runLLMIteration(ctx, agent, messages, opts, progressCallback)
	if err != nil {
		return "", err
	}

	// If last tool had ForUser content and we already sent it, we might not need to send final response
	// This is controlled by the tool's Silent flag and ForUser content

	// 5. Handle empty response
	if finalContent == "" {
		// Instead of returning a hardcoded default response, generate a meaningful summary
		// using the LLM based on the conversation history and user's original query

		// Create a summary prompt to generate a meaningful response
		summaryPrompt := fmt.Sprintf("Based on our conversation and the tasks I attempted to complete, please provide a helpful summary. The user asked: '%s'. If you performed any actions or found any information, please summarize that. If you couldn't find the requested information, explain that politely and suggest possible next steps.", opts.UserMessage)

		// Build a new message list with the summary prompt
		var summaryHistory []providers.Message
		if al.sessionManager != nil {
			summaryHistory = al.sessionManager.GetHistory(opts.SessionKey)
		} else {
			summaryHistory = agent.Sessions.GetHistory(opts.SessionKey)
		}

		var summaryMessages []providers.Message
		if al.messageBuilder != nil {
			// 需要转换配置类型
			compactionConfig := CompactionConfig{
				ReserveTokens:       agent.CompactionConfig.ReserveTokens,
				KeepRecentTokens:    agent.CompactionConfig.KeepRecentTokens,
				ReserveTokensFloor:  agent.CompactionConfig.ReserveTokensFloor,
				MemoryFlushEnabled:  agent.CompactionConfig.MemoryFlush.Enabled,
				SoftThresholdTokens: agent.CompactionConfig.MemoryFlush.SoftThresholdTokens,
			}
			summaryMessages = al.messageBuilder.BuildMessagesWithConfig(
				summaryHistory,
				agent.Sessions.GetSummary(opts.SessionKey),
				summaryPrompt,
				"", // memoryContext
				"", // media
				opts.Channel,
				opts.ChatID,
				compactionConfig, // 使用转换后的配置
			)
		} else {
			summaryMessages = agent.ContextBuilder.BuildMessages(
				summaryHistory,
				agent.Sessions.GetSummary(opts.SessionKey),
				summaryPrompt,
				"", // memoryContext
				nil, // media
				opts.Channel,
				opts.ChatID,
			)
		}

		// Call the LLM to generate a meaningful response
		summaryResponse, err := agent.Provider.Chat(ctx, summaryMessages, agent.Tools.ToProviderDefs(), agent.Model, map[string]any{
			"max_tokens":  agent.MaxTokens,
			"temperature": agent.Temperature,
		})

		if err != nil || summaryResponse == nil || summaryResponse.Content == "" {
			// If LLM fails to generate a response, provide a more helpful default
			finalContent = fmt.Sprintf("I've completed processing your request about: '%s'. I couldn't generate a detailed response, but I may have performed some actions in the background. If you need specific information, please try rephrasing your question.",
				utils.Truncate(opts.UserMessage, 60))
		} else {
			finalContent = summaryResponse.Content
		}
	}

	// 6. Save final assistant message to session
	if al.sessionManager != nil {
		al.sessionManager.AddAssistantMessage(opts.SessionKey, finalContent)
		al.sessionManager.Save(opts.SessionKey)
	} else {
		agent.Sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
		agent.Sessions.Save(opts.SessionKey)
	}

	// 新增：将重要信息沉淀到记忆系统
	if al.memoryManager != nil && finalContent != "" {
		go func() { // 异步沉淀以避免阻塞响应
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel() // 使用defer确保cancel被调用
			_ = ctx // 显式使用ctx变量避免未使用警告
			// 使用SedimentKnowledge方法沉淀知识
			err := al.memoryManager.SedimentKnowledge(
				fmt.Sprintf("Session: %s, Query: %s, Response: %s", opts.SessionKey, opts.UserMessage, finalContent),
				opts.SessionKey, // sourceKey
			)
			if err != nil {
				logger.WarnCF("memory", "Failed to sediment knowledge", map[string]any{"error": err, "session_key": opts.SessionKey})
			}
		}()
	}

	// 7. Optional: summarization
	if opts.EnableSummary {
		al.maybeSummarize(agent, opts.SessionKey, opts.Channel, opts.ChatID)
	}

	// 8. Optional: send response via bus - now always handled by caller
	// Previously used opts.SendResponse to control direct sending from here,
	// but now the caller (processMessage) handles response sending to avoid duplication.

	// 9. Log response
	responsePreview := utils.Truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
		map[string]any{
			"agent_id":     agent.ID,
			"session_key":  opts.SessionKey,
			"iterations":   iteration,
			"final_length": len(finalContent),
		})

	return finalContent, nil
}

// runLLMIteration executes the LLM call loop with tool handling.
// This implements the core perception-decision-action-feedback loop
func (al *AgentLoop) runLLMIteration(
	ctx context.Context,
	agent *AgentInstance,
	messages []providers.Message,
	opts processOptions,
	progressCallback ProgressCallback,
) (string, int, error) {
	iteration := 0
	var finalContent string

	logger.InfoCF("agent", "Starting LLM iteration loop",
		map[string]any{
			"max_iterations": agent.MaxIterations,
			"message_count":  len(messages),
			"session_key":    opts.SessionKey,
		})

	for iteration < agent.MaxIterations {
		iteration++

		logger.InfoCF("agent", fmt.Sprintf("Starting iteration %d", iteration),
			map[string]any{
				"iteration":   iteration,
				"message_len": len(messages),
				"session_key": opts.SessionKey,
			})

		// Core LLM interaction loop - the essential logic
		response, err := al.callLLMWithFallback(ctx, agent, messages, agent.Tools.ToProviderDefs(), opts)
		if err != nil {
			logger.ErrorCF("agent", "LLM call failed", map[string]any{"error": err, "iteration": iteration})
			return "", iteration, fmt.Errorf("LLM call failed: %w", err)
		}

		logger.InfoCF("agent", fmt.Sprintf("LLM iteration %d completed", iteration),
			map[string]any{
				"iteration":      iteration,
				"tool_call_count": len(response.ToolCalls),
				"content_length": len(response.Content),
				"agent_id":       agent.ID,
				"session_key":    opts.SessionKey,
				"has_content":    response.Content != "",
				"has_tools":      len(response.ToolCalls) > 0,
			})

		// Check if no tool calls - we're done
		if len(response.ToolCalls) == 0 {
			logger.InfoCF("agent", "No tool calls, exiting iteration loop",
				map[string]any{
					"iteration":   iteration,
					"session_key": opts.SessionKey,
					"content_len": len(response.Content),
				})
			finalContent = response.Content
			break
		}

		// Process tool calls and integrate results back into messages
		updatedMessages, err := al.processAndIntegrateToolCalls(ctx, agent, response, messages, opts, progressCallback)
		if err != nil {
			logger.ErrorCF("agent", "Failed to process tool calls", map[string]any{"error": err, "iteration": iteration})
			return "", iteration, fmt.Errorf("failed to process tool calls: %w", err)
		}
		messages = updatedMessages

		logger.InfoCF("agent", "Tool calls processed, continuing to next iteration",
			map[string]any{
				"iteration":        iteration,
				"message_count":    len(messages),
				"session_key":      opts.SessionKey,
				"tool_calls_count": len(response.ToolCalls),
			})
			// Additional verification: Log the last few messages to ensure tool results are present 
			if len(messages) >= 2 { 
				lastMsg := messages[len(messages)-1] 
				secondLastMsg := messages[len(messages)-2] 
				logger.InfoCF("agent", "Last two messages in sequence", 
					map[string]any{ 
						"second_last_role": secondLastMsg.Role, 
						"second_last_tool_call_count": len(secondLastMsg.ToolCalls), 
						"last_role":        lastMsg.Role, 
						"last_tool_call_id": lastMsg.ToolCallID, 
						"last_content_preview": utils.Truncate(lastMsg.Content, 100), 
					}) 
			}

		// Check for steering/interruption
		if steering, ok := al.bus.ConsumeSteeringForSession(opts.SessionKey); ok {
			userMsg := providers.Message{
				Role:    "user",
				Content: fmt.Sprintf("[User interrupted]: %s", steering.Content),
			}
			messages = append(messages, userMsg)
			logger.InfoCF("agent", "User interruption detected, adding steering message",
				map[string]any{
					"session_key": opts.SessionKey,
				})
			continue
		}
	}

	logger.InfoCF("agent", "LLM iteration loop completed",
		map[string]any{
			"final_iteration": iteration,
			"session_key":     opts.SessionKey,
			"final_content":   len(finalContent),
		})

	// Final check: if we've exhausted iterations but still have content from the last response
	if iteration >= agent.MaxIterations && finalContent == "" && len(messages) > 0 {
		// Try to get the content from the last assistant message if available
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" && messages[i].Content != "" {
				finalContent = messages[i].Content
				break
			}
		}
	}

	return finalContent, iteration, nil
}

// processAndIntegrateToolCalls handles tool execution and integrates results back to the conversation
func (al *AgentLoop) processAndIntegrateToolCalls(
	ctx context.Context,
	agent *AgentInstance,
	response *providers.LLMResponse,
	messages []providers.Message,
	opts processOptions,
	progressCallback ProgressCallback,
) ([]providers.Message, error) {
	logger.InfoCF("agent", "Processing and integrating tool calls",
		map[string]any{
			"tool_call_count": len(response.ToolCalls),
			"message_count_before": len(messages),
			"session_key":     opts.SessionKey,
		})

	// Add assistant message with tool calls to messages
	assistantMsg := al.createAssistantMessage(response)
	updatedMessages := append(messages, assistantMsg)

	logger.InfoCF("agent", "Added assistant message with tool calls",
		map[string]any{
			"message_count_after_assistant": len(updatedMessages),
			"session_key":                   opts.SessionKey,
		})

	// Save assistant message with tool calls to session - critical for history consistency.
	// Without this, reloaded history has orphaned tool results (no preceding assistant+tool_calls),
	// causing sanitizeHistoryForProvider to drop them and the LLM to repeat tool calls.
	if al.sessionManager != nil {
		al.sessionManager.AddToolResult(opts.SessionKey, assistantMsg)
	} else {
		agent.Sessions.AddFullMessage(opts.SessionKey, assistantMsg)
	}

	// Process tool calls and get results
	toolResultMessages, err := al.processToolCalls(ctx, agent, response.ToolCalls, opts, progressCallback)
	if err != nil {
		logger.ErrorCF("agent", "Failed to process tool calls", map[string]any{"error": err})
		return nil, err
	}

	// Add tool results to messages
	for _, toolResultMsg := range toolResultMessages {
		updatedMessages = append(updatedMessages, toolResultMsg)
		logger.InfoCF("agent", "Added tool result message to updated messages",
			map[string]any{
				"tool_call_id":  toolResultMsg.ToolCallID,
				"content_len":   len(toolResultMsg.Content),
				"role":          toolResultMsg.Role,
				"message_count": len(updatedMessages),
				"session_key":   opts.SessionKey,
			})
	}

	logger.InfoCF("agent", "Completed processing and integrating tool calls",
		map[string]any{
			"message_count_final": len(updatedMessages),
			"tool_result_count":   len(toolResultMessages),
			"session_key":         opts.SessionKey,
		})

	return updatedMessages, nil
}
// processToolCalls handles the execution of tool calls with all necessary processing
func (al *AgentLoop) processToolCalls(
	ctx context.Context,
	agent *AgentInstance,
	toolCalls []providers.ToolCall,
	opts processOptions,
	progressCallback ProgressCallback,
) ([]providers.Message, error) {
	logger.InfoCF("agent", "Processing tool calls in sequence",
		map[string]any{
			"tool_call_count": len(toolCalls),
			"session_key":     opts.SessionKey,
			"channel":         opts.Channel,
			"chat_id":         opts.ChatID,
		})

	// Use ToolHandler to process the tool calls
	toolResultMessages, err := al.toolHandler.ProcessToolCalls(
		ctx,
		toolCalls,
		opts.Channel,
		opts.ChatID,
		progressCallback,
		opts.SessionKey,
	)
	if err != nil {
		logger.ErrorCF("agent", "Failed to process tool calls via handler", map[string]any{"error": err})
		return nil, err
	}

	logger.InfoCF("agent", "Tool handler completed",
		map[string]any{
			"result_message_count": len(toolResultMessages),
			"session_key":          opts.SessionKey,
		})

	// Handle memory sedimentation for each tool result
	for _, toolResultMsg := range toolResultMessages {
		// Find the corresponding tool call to get the name
		for _, tc := range toolCalls {
			if tc.ID == toolResultMsg.ToolCallID {
				al.sedimentToolResult(toolResultMsg.Content, tc, opts)
				break
			}
		}

		// Save to session - this is critical for ensuring LLM sees results in next iteration
		if al.sessionManager != nil {
			al.sessionManager.AddToolResult(opts.SessionKey, toolResultMsg)
			logger.InfoCF("agent", "Saved tool result to session manager",
				map[string]any{
					"tool_call_id": toolResultMsg.ToolCallID,
					"content_len":  len(toolResultMsg.Content),
					"session_key":  opts.SessionKey,
				})
		} else {
			agent.Sessions.AddFullMessage(opts.SessionKey, toolResultMsg)
			logger.InfoCF("agent", "Saved tool result to agent sessions",
				map[string]any{
					"tool_call_id": toolResultMsg.ToolCallID,
					"content_len":  len(toolResultMsg.Content),
					"session_key":  opts.SessionKey,
				})
		}
	}

	// Verify that the session has been updated correctly
	if al.sessionManager != nil {
		history := al.sessionManager.GetHistory(opts.SessionKey)
		logger.InfoCF("agent", "Session history verified after tool results",
			map[string]any{
				"session_key":   opts.SessionKey,
				"history_count": len(history),
				"last_msg_role": history[len(history)-1].Role,
				"last_msg_content_preview": utils.Truncate(history[len(history)-1].Content, 100),
			})
	} else {
		history := agent.Sessions.GetHistory(opts.SessionKey)
		logger.InfoCF("agent", "Agent session history verified after tool results",
			map[string]any{
				"session_key":   opts.SessionKey,
				"history_count": len(history),
				"last_msg_role": history[len(history)-1].Role,
				"last_msg_content_preview": utils.Truncate(history[len(history)-1].Content, 100),
			})
	}

	return toolResultMessages, nil
}

// createAsyncCallback creates the async callback for async tools
func (al *AgentLoop) createAsyncCallback(toolName string) func(context.Context, *tools.ToolResult) {
	return func(callbackCtx context.Context, result *tools.ToolResult) {
		if !result.Silent && result.ForUser != "" {
			logger.InfoCF("agent", "Async tool completed, agent will handle notification",
				map[string]any{
					"tool":        toolName,
					"content_len": len(result.ForUser),
				})
		}
	}
}

// handleToolResultNotification handles sending tool results to user if needed
func (al *AgentLoop) handleToolResultNotification(
	toolResult *tools.ToolResult,
	toolName string,
	opts processOptions,
	progressCallback ProgressCallback,
) {
	// Determine if tool result should be sent to user based on system configuration
	shouldSendToUser := al.shouldSendToolResultToUser(nil, toolName, toolResult) // Simplified version

	if shouldSendToUser && toolResult.ForUser != "" {
		al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: toolResult.ForUser,
		})

		// Additionally, for certain channels like Feishu, we may want to provide a clearer indication
		// that a tool has completed, especially when many tools are being executed
		progressContent := fmt.Sprintf("🔧 Tool '%s' completed: %s", toolName,
			utils.Truncate(toolResult.ForUser, 512))
		progressMsg := bus.OutboundMessage{
			Channel:     opts.Channel,
			ChatID:      opts.ChatID,
			Content:     progressContent,
			MessageType: bus.MessageTypeProgress,
		}

		// Publish progress message via the progress callback mechanism instead of directly
		if progressCallback != nil {
			progressCallback(0, progressContent, map[string]interface{}{
				"tool_name": toolName,
				"phase": "tool_completed",
			})
		} else {
			// If no progress callback, send directly as a progress message
			al.bus.PublishOutbound(progressMsg)
		}
	}
}

// sedimentToolResult handles sedimentation of important tool results to memory
func (al *AgentLoop) sedimentToolResult(contentForLLM string, tc providers.ToolCall, opts processOptions) {
	if al.memoryManager != nil && contentForLLM != "" && shouldRememberToolResult(tc.Name) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = ctx // Explicitly use ctx to avoid warnings
			// Create a summary of tool execution
			toolResultSummary := fmt.Sprintf("Tool '%s' executed with arguments %v, result: %s", tc.Name, tc.Arguments, contentForLLM)
			// Sediment tool execution results
			err := al.memoryManager.SedimentKnowledge(
				toolResultSummary,
				fmt.Sprintf("tool_result_%s", tc.Name),
			)
			if err != nil {
				logger.WarnCF("memory", "Failed to sediment tool result", map[string]any{"error": err, "tool": tc.Name})
			}
		}()
	}
}

// skipToolCall creates a tool result message for skipped calls due to user interruption.
func skipToolCall(tc providers.ToolCall) providers.Message {
	return providers.Message{
		Role:       "tool",
		Content:    "Skipped due to user interruption.",
		ToolCallID: tc.ID,
	}
}

// callLLMWithFallback handles calling LLM with potential fallback logic
func (al *AgentLoop) callLLMWithFallback(
	ctx context.Context,
	agent *AgentInstance,
	messages []providers.Message,
	providerToolDefs []providers.ToolDefinition,
	opts processOptions,
) (*providers.LLMResponse, error) {
	var response *providers.LLMResponse
	var err error

	logger.InfoCF("agent", "Calling LLM with tools",
		map[string]any{
			"tool_def_count": len(providerToolDefs),
			"message_count":  len(messages),
			"session_key":    opts.SessionKey,
			"model":          agent.Model,
		})

	callLLM := func() (*providers.LLMResponse, error) {
		if len(agent.Candidates) > 1 && al.fallback != nil {
			fbResult, fbErr := al.fallback.Execute(ctx, agent.Candidates,
				func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
					return agent.Provider.Chat(ctx, messages, providerToolDefs, model, map[string]any{
						"max_tokens":  agent.MaxTokens,
						"temperature": agent.Temperature,
					})
				},
			)
			if fbErr != nil {
				return nil, fbErr
			}
			if fbResult.Provider != "" && len(fbResult.Attempts) > 0 {
				logger.InfoCF("agent", fmt.Sprintf("Fallback: succeeded with %s/%s after %d attempts",
					fbResult.Provider, fbResult.Model, len(fbResult.Attempts)+1),
					map[string]any{"agent_id": agent.ID})
			}
			return fbResult.Response, nil
		}
		return agent.Provider.Chat(ctx, messages, providerToolDefs, agent.Model, map[string]any{
			"max_tokens":  agent.MaxTokens,
			"temperature": agent.Temperature,
		})
	}

	// Retry loop for context/token errors
	maxRetries := 3 // Increase retries for context overflow
	for retry := 0; retry <= maxRetries; retry++ {
		response, err = callLLM()
		if err == nil {
			logger.InfoCF("agent", "LLM call successful",
				map[string]any{
					"has_content":     response.Content != "",
					"tool_call_count": len(response.ToolCalls),
					"session_key":     opts.SessionKey,
				})
			break
		}

		if isContextOverflowError(err.Error()) || strings.Contains(strings.ToLower(err.Error()), "input length should be") {
			logger.WarnCF("agent", "Context window error detected, attempting compression", map[string]any{
				"error": err.Error(),
				"retry": retry,
			})

			if retry == 0 && !constants.IsInternalChannel(opts.Channel) {
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.ChatID,
					Content: "Context window exceeded. Compressing history and retrying...",
				})
			}

			// Use the improved compression based on modular architecture
			if al.sessionManager != nil {
				al.sessionManager.ForceCompression(opts.SessionKey)
				newHistory := al.sessionManager.GetHistory(opts.SessionKey)
				newSummary := al.sessionManager.GetSummary(opts.SessionKey)

				// Also use MessageBuilder if available
				if al.messageBuilder != nil {
					// Rebuild messages with the new, compressed history
					var rebuildMemoryContext string
					if al.memoryManager != nil {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel() // 确保取消上下文
						_ = ctx // 显式使用ctx变量避免未使用警告

						// 使用Search方法获取相关记忆上下文
						searchResults, err := al.memoryManager.Search(opts.UserMessage, 5) // 获取最多5个相关项
						if err != nil {
							logger.WarnCF("memory", "Failed to search memory context during rebuild", map[string]any{"error": err})
						} else if len(searchResults) > 0 {
							// 将搜索结果格式化为上下文字符串
							var contextBuilder strings.Builder
							contextBuilder.WriteString("## Relevant Past Memories\n")

							for i, result := range searchResults {
								if i >= 3 { // 限制最多3个最相关项目
									break
								}
								contextBuilder.WriteString(fmt.Sprintf("- %s\n", result.Content))
								contextBuilder.WriteString("\n\n")
							}

							rebuildMemoryContext = contextBuilder.String()
						}
					}

					// Build messages using MessageBuilder with proper config
					// 需要转换配置类型
					compactionConfig := CompactionConfig{
						ReserveTokens:       agent.CompactionConfig.ReserveTokens,
						KeepRecentTokens:    agent.CompactionConfig.KeepRecentTokens,
						ReserveTokensFloor:  agent.CompactionConfig.ReserveTokensFloor,
						MemoryFlushEnabled:  agent.CompactionConfig.MemoryFlush.Enabled,
						SoftThresholdTokens: agent.CompactionConfig.MemoryFlush.SoftThresholdTokens,
					}
					messages = al.messageBuilder.BuildMessagesWithConfig(
						newHistory, newSummary, opts.UserMessage,
						rebuildMemoryContext, "", opts.Channel, opts.ChatID,
						compactionConfig, // 使用转换后的配置
					)
				} else {
					// Fallback to the original method if MessageBuilder not available
					al.forceCompression(agent, opts.SessionKey)
					newHistory := agent.Sessions.GetHistory(opts.SessionKey)
					newSummary := agent.Sessions.GetSummary(opts.SessionKey)

					// Also retrieve relevant memory context for the rebuilt messages
					var rebuildMemoryContext string
					if al.memoryManager != nil {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel() // 确保取消上下文
						_ = ctx // 显式使用ctx变量避免未使用警告

						// 使用Search方法获取相关记忆上下文
						searchResults, err := al.memoryManager.Search(opts.UserMessage, 5) // 获取最多5个相关项
						if err != nil {
							logger.WarnCF("memory", "Failed to search memory context during rebuild", map[string]any{"error": err})
						} else if len(searchResults) > 0 {
							// 将搜索结果格式化为上下文字符串
							var contextBuilder strings.Builder
							contextBuilder.WriteString("## Relevant Past Memories\n")

							for i, result := range searchResults {
								if i >= 3 { // 限制最多3个最相关项目
									break
								}
								contextBuilder.WriteString(fmt.Sprintf("- %s\n", result.Content))
								contextBuilder.WriteString("\n\n")
							}

							rebuildMemoryContext = contextBuilder.String()
						}
					}

					messages = agent.ContextBuilder.BuildMessages(
						newHistory, newSummary, opts.UserMessage,
						rebuildMemoryContext, nil, opts.Channel, opts.ChatID,
					)
				}
			} else {
				// Original fallback method
				al.forceCompression(agent, opts.SessionKey)
				newHistory := agent.Sessions.GetHistory(opts.SessionKey)
				newSummary := agent.Sessions.GetSummary(opts.SessionKey)

				// IMPORTANT: Include the original user message when rebuilding messages after compression
				// Also retrieve relevant memory context for the rebuilt messages
				var rebuildMemoryContext string
				if al.memoryManager != nil {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel() // 确保取消上下文
					_ = ctx // 显式使用ctx变量避免未使用警告

					// 使用Search方法获取相关记忆上下文
					searchResults, err := al.memoryManager.Search(opts.UserMessage, 5) // 获取最多5个相关项
					if err != nil {
						logger.WarnCF("memory", "Failed to search memory context during rebuild", map[string]any{"error": err})
					} else if len(searchResults) > 0 {
						// 将搜索结果格式化为上下文字符串
						var contextBuilder strings.Builder
						contextBuilder.WriteString("## Relevant Past Memories\n")

						for i, result := range searchResults {
							if i >= 3 { // 限制最多3个最相关项目
								break
							}
							contextBuilder.WriteString(fmt.Sprintf("- %s\n", result.Content))
							contextBuilder.WriteString("\n\n")
						}

						rebuildMemoryContext = contextBuilder.String()
					}
				}

				messages = agent.ContextBuilder.BuildMessages(
					newHistory, newSummary, opts.UserMessage,
					rebuildMemoryContext, nil, opts.Channel, opts.ChatID,
				)
			}
			continue
		}
		break
	}

	return response, err
}

// createAssistantMessage creates an assistant message from the LLM response
func (al *AgentLoop) createAssistantMessage(response *providers.LLMResponse) providers.Message {
	assistantMsg := providers.Message{
		Role:    "assistant",
		Content: response.Content,
	}

	logger.InfoCF("agent", "Creating assistant message",
		map[string]any{
			"content_length": len(response.Content),
			"tool_call_count": len(response.ToolCalls),
			"has_content":    response.Content != "",
			"has_tools":      len(response.ToolCalls) > 0,
		})

	// Process tool calls
	normalizedToolCalls := make([]providers.ToolCall, 0, len(response.ToolCalls))
	for _, tc := range response.ToolCalls {
		logger.InfoCF("agent", "Normalizing tool call",
			map[string]any{
				"tool_call_id": tc.ID,
				"tool_name":    tc.Name,
				"arguments":    tc.Arguments,
			})
		normalizedToolCalls = append(normalizedToolCalls, providers.NormalizeToolCall(tc))
	}

	for _, tc := range normalizedToolCalls {
		argumentsJSON, _ := json.Marshal(tc.Arguments)
		// Copy ExtraContent to ensure thought_signature is persisted for Gemini 3
		extraContent := tc.ExtraContent
		thoughtSignature := ""
		if tc.Function != nil {
			thoughtSignature = tc.Function.ThoughtSignature
		}

		toolCallEntry := providers.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Name: tc.Name,
			Function: &providers.FunctionCall{
				Name:             tc.Name,
				Arguments:        string(argumentsJSON),
				ThoughtSignature: thoughtSignature,
			},
			ExtraContent:     extraContent,
			ThoughtSignature: thoughtSignature,
		}

		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, toolCallEntry)

		logger.InfoCF("agent", "Added tool call to assistant message",
			map[string]any{
				"tool_call_id": toolCallEntry.ID,
				"tool_name":    toolCallEntry.Name,
				"arguments_len": len(toolCallEntry.Function.Arguments),
			})
	}

	return assistantMsg
}

// updateToolContexts updates the context for tools that need channel/chatID info.
func (al *AgentLoop) updateToolContexts(agent *AgentInstance, channel, chatID string) {
	// Use ContextualTool interface instead of type assertions
	if tool, ok := agent.Tools.Get("message"); ok {
		if mt, ok := tool.(tools.ContextualTool); ok {
			mt.SetContext(channel, chatID)
		}
	}
	if tool, ok := agent.Tools.Get("spawn"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
	if tool, ok := agent.Tools.Get("subagent"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
}

// triggerMemoryFlush runs a silent memory flush before auto-compaction to ensure
// important data is saved to persistent storage
func (al *AgentLoop) triggerMemoryFlush(agent *AgentInstance, sessionKey, channel, chatID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistory(sessionKey)
	summary := agent.Sessions.GetSummary(sessionKey)

	// 获取相关的记忆上下文用于内存刷新
	var flushMemoryContext string
	if al.memoryManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel() // 确保取消上下文
		_ = ctx // 显式使用ctx变量避免未使用警告

		// 使用之前的对话作为查询来获取相关记忆
		previousConversation := fmt.Sprintf("Previous conversation summary: %s, Last few exchanges: %v", summary, al.getLastExchanges(history, 3))
		searchResults, err := al.memoryManager.Search(previousConversation, 3) // 获取最多3个相关项
		if err != nil {
			logger.WarnCF("memory", "Failed to search memory context during flush", map[string]any{"error": err})
		} else if len(searchResults) > 0 {
			// 将搜索结果格式化为上下文字符串
			var contextBuilder strings.Builder
			contextBuilder.WriteString("## Relevant Past Memories\n")

			for i, result := range searchResults {
				if i >= 2 { // 限制最多2个最相关项目以避免过多上下文
					break
				}
				contextBuilder.WriteString(fmt.Sprintf("- %s\n", result.Content))
				contextBuilder.WriteString("\n\n")
			}

			flushMemoryContext = contextBuilder.String()
		}
	}

	// Build a memory flush prompt that encourages the model to save important info
	memoryFlushPrompt := "Before we compact the conversation history, please save any important persistent information to your memory systems. " +
		"This is an automatic reminder to ensure no important data is lost during upcoming history compression. " +
		"You may reply with 'NO_REPLY' if no memory updates are needed."

	messages := agent.ContextBuilder.BuildMessages(
		history,
		summary,
		memoryFlushPrompt,
		flushMemoryContext,  // 现在使用获取的相关记忆上下文
		nil,     // media参数
		channel,
		chatID,
	)

	// Execute a silent call to encourage memory saving
	providerToolDefs := agent.Tools.ToProviderDefs()

	// Call LLM with memory flush prompt
	response, err := agent.Provider.Chat(ctx, messages, providerToolDefs, agent.Model, map[string]any{
		"max_tokens":  agent.MaxTokens,
		"temperature": agent.Temperature,
	})

	if err != nil {
		logger.WarnCF("agent", "Memory flush failed", map[string]any{
			"error": err.Error(),
			"session_key": sessionKey,
		})
		return
	}

	// Execute any tool calls that might save important information
	if len(response.ToolCalls) > 0 {
		// Add the assistant's memory flush response to the history
		assistantMsg := providers.Message{
			Role:    "assistant",
			Content: response.Content,
		}
		for _, tc := range response.ToolCalls {
			argumentsJSON, _ := json.Marshal(tc.Arguments)
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Name: tc.Name,
				Function: &providers.FunctionCall{
					Name:      tc.Name,
					Arguments: string(argumentsJSON),
				},
			})
		}

		// Execute the tool calls
		for _, tc := range response.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("agent", fmt.Sprintf("Memory flush tool call: %s(%s)", tc.Name, argsPreview),
				map[string]any{
					"agent_id":  agent.ID,
					"tool":      tc.Name,
				})

			toolResult := agent.Tools.ExecuteWithContext(
				ctx,
				tc.Name,
				tc.Arguments,
				channel,
				chatID,
				nil, // No async callback for memory flush
			)

			// Process the tool result
			contentForLLM := toolResult.ForLLM
			if contentForLLM == "" && toolResult.Err != nil {
				contentForLLM = toolResult.Err.Error()
			}

			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}

			// Add to session for potential future reference
			agent.Sessions.AddFullMessage(sessionKey, toolResultMsg)

			// 新增：如果工具结果重要，则将其沉淀到记忆系统（内存刷新场景）
			if al.memoryManager != nil && contentForLLM != "" && shouldRememberToolResult(tc.Name) {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel() // 使用defer确保cancel被调用
					_ = ctx // 显式使用ctx变量避免未使用警告
					// 创建一个描述工具调用和结果的摘要
					toolResultSummary := fmt.Sprintf("Memory flush tool '%s' executed with arguments %v, result: %s", tc.Name, tc.Arguments, contentForLLM)
					// 使用SedimentKnowledge方法沉淀工具执行结果
					err := al.memoryManager.SedimentKnowledge(
						toolResultSummary,
						fmt.Sprintf("memory_flush_tool_result_%s", tc.Name), // sourceKey
					)
					if err != nil {
						logger.WarnCF("memory", "Failed to sediment memory flush tool result", map[string]any{"error": err, "tool": tc.Name})
					}
				}()
			}
		}
	}
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
func (al *AgentLoop) maybeSummarize(agent *AgentInstance, sessionKey, channel, chatID string) {
	newHistory := agent.Sessions.GetHistory(sessionKey)
	tokenEstimate := al.estimateTokens(newHistory)

	// Check if we should trigger a pre-compaction memory flush
	if agent.CompactionConfig.MemoryFlush.Enabled {
		reserveTokensFloor := agent.CompactionConfig.ReserveTokensFloor
		softThresholdTokens := agent.CompactionConfig.MemoryFlush.SoftThresholdTokens
		memoryFlushThreshold := agent.ContextWindow - reserveTokensFloor - softThresholdTokens

		if tokenEstimate > memoryFlushThreshold {
			// Trigger a silent memory flush to save important data before auto-compaction
			go al.triggerMemoryFlush(agent, sessionKey, channel, chatID)
		}
	}

	// OLD AUTO-COMPACTION LOGIC COMMENTED OUT:
	// The auto-compaction feature has been removed in favor of the more intelligent
	// session pruning mechanism. The pre-compaction memory flush above remains
	// to ensure important data is saved before potential manual or error-driven
	// compression events.
	/*
	if len(newHistory) > 20 || tokenEstimate > threshold {
		summarizeKey := agent.ID + ":" + sessionKey
		if _, loading := al.summarizing.LoadOrStore(summarizeKey, true); !loading {
			go func() {
				defer al.summarizing.Delete(summarizeKey)
				if !constants.IsInternalChannel(channel) {
					al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: channel,
						ChatID:  chatID,
						Content: "Memory threshold reached. Optimizing conversation history...",
					})
				}
				al.summarizeSession(agent, sessionKey)
			}()
		}
	}
	*/
}

// forceCompression aggressively reduces context when the limit is hit.
// It drops the oldest 50% of messages (keeping system prompt and last user message).
// This version preserves tool call and tool result message pairs to maintain proper message sequence.
func (al *AgentLoop) forceCompression(agent *AgentInstance, sessionKey string) {
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) <= 4 {
		return
	}

	// Identify tool call-result pairs to preserve
	// Group messages by tool call relationships to maintain integrity
	groupedMessages := al.groupRelatedMessages(history)

	// Calculate how many groups to keep (preserve tool call/result relationships)
	keepCount := len(groupedMessages) / 4 // Keep only 1/4 of the message groups for more aggressive compression
	if keepCount < 2 {
		keepCount = 2 // Ensure we keep at least 2 groups
	}

	// Start with system message if present, then take the most recent groups
	var newHistory []providers.Message

	// Preserve system message at the beginning
	if len(history) > 0 && history[0].Role == "system" {
		// Extract any compression notes that might already be present
		var baseContent string
		contentParts := strings.Split(history[0].Content, "\n\n[System Note:")
		if len(contentParts) > 0 {
			baseContent = contentParts[0]
		}
		// Create updated system message with compression notice
		compressionNotice := fmt.Sprintf("\n\n[System Note: History compressed at %s due to length. Earlier context preserved in context but truncated for current exchange. Multiple compressions applied.]", time.Now().Format("2006-01-02 15:04:05"))
		newHistory = append(newHistory, providers.Message{
			Role:    "system",
			Content: baseContent + compressionNotice,
		})
	}

	// Add remaining message groups (most recent ones)
	startIdx := len(groupedMessages) - keepCount
	if startIdx < 0 {
		startIdx = 0
	}

	for i := startIdx; i < len(groupedMessages); i++ {
		newHistory = append(newHistory, groupedMessages[i]...)
	}

	// Update session with compressed history
	agent.Sessions.SetHistory(sessionKey, newHistory)
	agent.Sessions.Save(sessionKey)
}

// min is a helper function for integer minimum
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// estimateTokens estimates the number of tokens in a message list.
// Uses a safe heuristic of 2.5 characters per token to account for CJK and other
// overheads better than the previous 3 chars/token.
func (al *AgentLoop) estimateTokens(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += utf8.RuneCountInString(m.Content)
	}
	// 2.5 chars per token = totalChars * 2 / 5
	return totalChars * 2 / 5
}

// pruneSessionMemory performs in-memory trimming of old tool results without
// rewriting the persistent history, similar to openclaw's session pruning mechanism
func (al *AgentLoop) pruneSessionMemory(agent *AgentInstance, sessionKey string, messages []providers.Message) []providers.Message {
	// Only perform pruning if we have compaction config available
	compactionConfig := agent.CompactionConfig

	// If keepRecentTokens is 0, skip pruning
	if compactionConfig.KeepRecentTokens <= 0 {
		return messages
	}

	// Enhanced logic: preserve tool call-result pairs together
	var prunedMessages []providers.Message
	var accumulatedTokens int

	// Process messages in reverse order (most recent first)
	// This allows us to keep recent tool call-result pairs together
	i := len(messages) - 1
	for i >= 0 {
		msg := messages[i]
		msgTokens := al.estimateTokens([]providers.Message{msg})

		// Always keep user and assistant messages
		if msg.Role == "user" || msg.Role == "assistant" {
			// Check if this is an assistant message with tool calls
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				// Look ahead to find associated tool results
				// Add this assistant message
				prunedMessages = append(prunedMessages, msg)
				accumulatedTokens += msgTokens

				// Find immediate following tool results that belong to this assistant message
				j := i - 1
				for j >= 0 {
					nextMsg := messages[j]
					if nextMsg.Role == "tool" && nextMsg.ToolCallID != "" {
						// Check if this tool result corresponds to one of the tool calls
						toolCallFound := false
						for _, tc := range msg.ToolCalls {
							if tc.ID == nextMsg.ToolCallID {
								toolCallFound = true
								break
							}
						}
						if toolCallFound {
							toolMsgTokens := al.estimateTokens([]providers.Message{nextMsg})
							// Check if we can fit this tool result within our token budget
							if accumulatedTokens + toolMsgTokens < compactionConfig.KeepRecentTokens {
								prunedMessages = append(prunedMessages, nextMsg)
								accumulatedTokens += toolMsgTokens
								j--
							} else {
								// Can't fit this tool result, so break and stop adding
								break
							}
						} else {
							// This tool result doesn't belong to current assistant's tool calls
							break
						}
					} else {
						// Different role, stop looking for tool results
						break
					}
				}

				// Update i to skip processed messages
				i = j
			} else {
				// Regular user/assistant message, check if fits in token budget
				if accumulatedTokens + msgTokens < compactionConfig.KeepRecentTokens {
					prunedMessages = append(prunedMessages, msg)
					accumulatedTokens += msgTokens
					i--
				} else {
					// Token budget exceeded, stop processing
					break
				}
			}
		} else if msg.Role == "tool" {
			// For tool messages, check if we should keep them based on the keepRecentTokens config
			if accumulatedTokens + msgTokens < compactionConfig.KeepRecentTokens {
				// Look for the corresponding assistant message that initiated this tool call
				prunedMessages = append(prunedMessages, msg)
				accumulatedTokens += msgTokens
				i--
			} else {
				// Token budget exceeded, stop processing
				break
			}
		} else {
			// Other message types (system, etc.) - include if space permits
			if accumulatedTokens + msgTokens < compactionConfig.KeepRecentTokens {
				prunedMessages = append(prunedMessages, msg)
				accumulatedTokens += msgTokens
				i--
			} else {
				// Token budget exceeded, stop processing
				break
			}
		}
	}

	// Reverse the slice back to original chronological order
	for i, j := 0, len(prunedMessages)-1; i < j; i, j = i+1, j-1 {
		prunedMessages[i], prunedMessages[j] = prunedMessages[j], prunedMessages[i]
	}

	// Log if we pruned any messages
	originalCount := len(messages)
	prunedCount := len(prunedMessages)
	if originalCount > prunedCount {
		logger.InfoCF("agent", "Session pruning completed",
			map[string]any{
				"session_key": sessionKey,
				"original_messages": originalCount,
				"pruned_messages": prunedCount,
				"removed_messages": originalCount - prunedCount,
			})
	}

	return prunedMessages
}

// shouldRememberToolResult determines if a tool result should be remembered in memory
func shouldRememberToolResult(toolName string) bool {
	// Define tools whose results should be remembered
	rememberTools := map[string]bool{
		"write_file":   true,
		"append_file":  true,
		"mkdir":        true,
		"save_memory":  true,
		"web_search":   true,
		"browser_get":  true,
		"read_file":    true,
		"list_files":   true,
	}
	
	return rememberTools[toolName]
}

// hasRecentToolActivity checks if there has been recent tool activity
func hasRecentToolActivity(messages []providers.Message, recentCount int) bool {
	count := 0
	for i := len(messages) - 1; i >= 0 && count < recentCount; i-- {
		if messages[i].Role == "tool" {
			return true
		}
		count++
	}
	return false
}

// isContextOverflowError detects various forms of context overflow errors from different LLM providers
func isContextOverflowError(errorStr string) bool {
	lowerError := strings.ToLower(errorStr)

	// Basic token/context related errors
	if strings.Contains(lowerError, "token") ||
		strings.Contains(lowerError, "context") ||
		strings.Contains(lowerError, "maximum") ||
		strings.Contains(lowerError, "length") ||
		strings.Contains(lowerError, "truncate") {
		return true
	}

	// Specific provider error messages
	contextKeywords := []string{
		"max_tokens_exceeded",
		"context_length",
		"input_too_long",
		"request_too_large",
		"model_input_too_long",
		"exceeds maximum",
	}

	for _, keyword := range contextKeywords {
		if strings.Contains(lowerError, keyword) {
			return true
		}
	}

	return false
}

// groupRelatedMessages groups messages by their relationships (especially tool call-result pairs)
func (al *AgentLoop) groupRelatedMessages(messages []providers.Message) [][]providers.Message {
	var groups [][]providers.Message
	var currentGroup []providers.Message

	for i, msg := range messages {
		currentGroup = append(currentGroup, msg)

		// If this is a tool message, close the group
		if msg.Role == "tool" {
			// Find the corresponding assistant message that initiated the tool call
			for j := len(currentGroup) - 1; j >= 0; j-- {
				if currentGroup[j].Role == "assistant" && len(currentGroup[j].ToolCalls) > 0 {
					// Check if any of the assistant's tool calls matches this tool's call ID
					for _, tc := range currentGroup[j].ToolCalls {
						if tc.ID == msg.ToolCallID {
							groups = append(groups, currentGroup)
							currentGroup = []providers.Message{}
							break
						}
					}
					break
				}
			}
		} else if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			// Wait for the corresponding tool results
			continue
		} else {
			// For user/system messages, close the group if it's not part of a tool call chain
			if i+1 < len(messages) && messages[i+1].Role != "tool" {
				groups = append(groups, currentGroup)
				currentGroup = []providers.Message{}
			}
		}
	}

	// Add remaining messages as a group
	if len(currentGroup) > 0 {
		groups = append(groups, currentGroup)
	}

	return groups
}

// getLastExchanges returns the last N exchanges (user-assistant pairs) from the history
func (al *AgentLoop) getLastExchanges(history []providers.Message, n int) []providers.Message {
	var exchanges []providers.Message
	userMsgFound := false
	
	// Process from the end backwards
	for i := len(history) - 1; i >= 0 && n > 0; i-- {
		msg := history[i]
		
		if msg.Role == "user" {
			userMsgFound = true
			exchanges = append([]providers.Message{msg}, exchanges...)
			n--
		} else if msg.Role == "assistant" && userMsgFound {
			exchanges = append([]providers.Message{msg}, exchanges...)
			userMsgFound = false
		} else if msg.Role == "tool" {
			// Include tool messages that are part of the exchange
			exchanges = append([]providers.Message{msg}, exchanges...)
		} else if msg.Role == "system" {
			// Include system message if it's the first one
			if len(exchanges) == 0 || exchanges[0].Role != "system" {
				exchanges = append([]providers.Message{msg}, exchanges...)
			}
		}
	}
	
	return exchanges
}

// shouldSendToolResultToUser determines if tool result should be sent to user
func (al *AgentLoop) shouldSendToolResultToUser(agent *AgentInstance, toolName string, result *tools.ToolResult) bool {
	// If tool is marked as silent, don't send to user
	if result.Silent {
		return false
	}

	// For specific tools, we may have more refined rules, but for now, send if not silent
	return true
}

// handleCommand processes special commands
func (al *AgentLoop) handleCommand(ctx context.Context, msg bus.InboundMessage) (string, bool) {
	content := strings.TrimSpace(msg.Content)
	if !strings.HasPrefix(content, "/") {
		return "", false
	}

	parts := strings.Fields(content)
	if len(parts) == 0 {
		return "", false
	}

	cmd := parts[0]

	switch cmd {
	case "/ping":
		return "pong", true
	case "/help":
		helpText := "Available commands:\n" +
			"- /ping: Check if bot is alive\n" +
			"- /help: Show this help message\n" +
			"- /reset: Reset the conversation\n" +
			"- /status: Show system status"
		return helpText, true
	case "/reset":
		// Reset session by clearing history
		if al.registry != nil {
			defaultAgent := al.registry.GetDefaultAgent()
			if defaultAgent != nil {
				sessionKey := msg.Channel + "_" + msg.ChatID
				defaultAgent.Sessions.SetHistory(sessionKey, []providers.Message{})
				defaultAgent.Sessions.Save(sessionKey)
			}
		}
		return "Conversation has been reset.", true
	case "/status":
		status := fmt.Sprintf("Status: AgentLoop running\nActive sessions: %d", al.getActiveSessionCount())
		return status, true
	default:
		return fmt.Sprintf("Unknown command: %s. Type /help for available commands.", cmd), true
	}
}

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]any {
	info := make(map[string]any)

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		return info
	}

	// Tools info
	toolsList := agent.Tools.List()
	info["tools"] = map[string]any{
		"count": len(toolsList),
		"names": toolsList,
	}

	// Skills info
	info["skills"] = agent.ContextBuilder.GetSkillsInfo()

	// Agents info
	info["agents"] = map[string]any{
		"count": len(al.registry.ListAgentIDs()),
		"ids":   al.registry.ListAgentIDs(),
	}

	return info
}

// getActiveSessionCount returns the number of active sessions
func (al *AgentLoop) getActiveSessionCount() int {
	count := 0
	al.activeSessions.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// extractPeer extracts peer identifier from message
func extractPeer(msg bus.InboundMessage) *routing.RoutePeer {
	if peerID, exists := msg.Metadata["peer_id"]; exists && peerID != "" {
		return &routing.RoutePeer{Kind: "direct", ID: peerID}
	}
	if userID, exists := msg.Metadata["user_id"]; exists && userID != "" {
		return &routing.RoutePeer{Kind: "direct", ID: userID}
	}
	if accountID, exists := msg.Metadata["account_id"]; exists && accountID != "" {
		return &routing.RoutePeer{Kind: "direct", ID: accountID}
	}
	return &routing.RoutePeer{Kind: "direct", ID: msg.ChatID}
}

// extractParentPeer extracts parent peer identifier from message (for group/parent relationships)
func extractParentPeer(msg bus.InboundMessage) *routing.RoutePeer {
	if parentPeerID, exists := msg.Metadata["parent_peer_id"]; exists && parentPeerID != "" {
		return &routing.RoutePeer{Kind: "group", ID: parentPeerID}
	}
	if parentID, exists := msg.Metadata["parent_id"]; exists && parentID != "" {
		return &routing.RoutePeer{Kind: "group", ID: parentID}
	}
	if channelID, exists := msg.Metadata["channel_id"]; exists && channelID != "" {
		return &routing.RoutePeer{Kind: "channel", ID: channelID}
	}
	return &routing.RoutePeer{Kind: "channel", ID: msg.Channel + "_" + msg.ChatID}
}
