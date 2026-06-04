// QuantClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 QuantClaw contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sipeed/quantclaw/pkg/agent/memory"
	"github.com/sipeed/quantclaw/pkg/bus"
	"github.com/sipeed/quantclaw/pkg/channels"
	"github.com/sipeed/quantclaw/pkg/config"
	"github.com/sipeed/quantclaw/pkg/constants"
	"github.com/sipeed/quantclaw/pkg/logger"
	"github.com/sipeed/quantclaw/pkg/providers"
	"github.com/sipeed/quantclaw/pkg/routing"
	"github.com/sipeed/quantclaw/pkg/skills"
	"github.com/sipeed/quantclaw/pkg/state"
	"github.com/sipeed/quantclaw/pkg/tools"
	"github.com/sipeed/quantclaw/pkg/utils"
)

type AgentLoop struct {
	bus            *bus.MessageBus
	cfg            *config.Config
	registry       *AgentRegistry
	state          *state.Manager
	memoryManager  *memory.MemoryManager // 新增：内存管理器
	running        atomic.Bool
	summarizing    sync.Map
	inference      *InferenceService
	contextBudget  *ContextBudgetManager
	channelManager *channels.Manager
	activeSessions sync.Map // tracks sessions currently being processed

	// Modular architecture components placeholder for future Beehive integration
	useModular  bool         // Feature flag to control which system is used
	toolHandler *ToolHandler // 新增：工具处理器
}

// ProgressCallback is a function type for reporting progress during agent execution
type ProgressCallback func(progress float64, message string, metadata map[string]interface{}) error

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey      string // Session identifier for history/context
	Channel         string // Target channel for tool execution
	ChatID          string // Target chat ID for tool execution
	UserMessage     string // User message content (may include prefix)
	MemoryContext   string // Retrieved cross-session context for this turn
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
				"channel":  channel,
				"chat_id":  chatID,
				"content":  message,
				"progress": progress,
				"metadata": stringMetadata,
			})
			al.bus.PublishOutbound(progressMsg)
		} else {
			logger.DebugCF("agent", "Progress sending disabled, skipping progress message", map[string]any{
				"channel":  channel,
				"chat_id":  chatID,
				"content":  message,
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
	contextBudget := NewContextBudgetManager()
	inferenceService := NewInferenceService(msgBus, fallbackChain, contextBudget)

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
	toolHandler := NewToolHandler(cfg, msgBus)

	return &AgentLoop{
		bus:           msgBus,
		cfg:           cfg,
		registry:      registry,
		state:         stateManager,
		memoryManager: memoryManager, // 添加到实例
		summarizing:   sync.Map{},
		inference:     inferenceService,
		contextBudget: contextBudget,
		// Initialize modular architecture support but default to disabled
		useModular:  useModular,
		toolHandler: toolHandler, // 添加ToolHandler
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
		history = agent.SessionManager.GetHistory(opts.SessionKey)
		summary = agent.SessionManager.GetSummary(opts.SessionKey)
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
			memoryContext = formatMemoryContext(searchResults, 3)
		}
		cancel()
	}

	opts.MemoryContext = memoryContext

	// 构建消息
	messages := al.contextBudget.BuildMessages(
		agent,
		history,
		summary,
		opts.UserMessage,
		memoryContext,
		opts.Channel,
		opts.ChatID,
	)

	// 3. Save user message to session
	agent.SessionManager.AddUserMessage(opts.SessionKey, opts.UserMessage)

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
		summaryHistory = agent.SessionManager.GetHistory(opts.SessionKey)

		summaryMessages := al.contextBudget.BuildMessages(
			agent,
			summaryHistory,
			agent.SessionManager.GetSummary(opts.SessionKey),
			summaryPrompt,
			"",
			opts.Channel,
			opts.ChatID,
		)

		// Call the LLM to generate a meaningful response
		summaryResponse, err := al.inference.Invoke(ctx, agent, InferenceRequest{
			Messages:         summaryMessages,
			ProviderToolDefs: agent.Tools.ToProviderDefs(),
			Options: processOptions{
				SessionKey:    opts.SessionKey,
				Channel:       opts.Channel,
				ChatID:        opts.ChatID,
				UserMessage:   summaryPrompt,
				MemoryContext: "",
			},
		})

		if err != nil || summaryResponse == nil || summaryResponse.Response == nil || summaryResponse.Response.Content == "" {
			// If LLM fails to generate a response, provide a more helpful default
			finalContent = fmt.Sprintf("I've completed processing your request about: '%s'. I couldn't generate a detailed response, but I may have performed some actions in the background. If you need specific information, please try rephrasing your question.",
				utils.Truncate(opts.UserMessage, 60))
		} else {
			finalContent = summaryResponse.Response.Content
		}
	}

	// 6. Save final assistant message to session
	agent.SessionManager.AddAssistantMessage(opts.SessionKey, finalContent)
	agent.SessionManager.Save(opts.SessionKey)

	// 新增：将重要信息沉淀到记忆系统
	if al.memoryManager != nil && finalContent != "" {
		go func() { // 异步沉淀以避免阻塞响应
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel() // 使用defer确保cancel被调用
			_ = ctx        // 显式使用ctx变量避免未使用警告
			// 使用SedimentKnowledge方法沉淀知识
			err := al.memoryManager.SedimentKnowledgeWithType(
				fmt.Sprintf("Session: %s, Query: %s, Response: %s", opts.SessionKey, opts.UserMessage, finalContent),
				opts.SessionKey, // sourceKey
				memory.MemoryTypeConversationSummary,
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
		inferenceResult, err := al.inference.Invoke(ctx, agent, InferenceRequest{
			Messages:         messages,
			ProviderToolDefs: agent.Tools.ToProviderDefs(),
			Options:          opts,
		})
		if err != nil {
			logger.ErrorCF("agent", "LLM call failed", map[string]any{"error": err, "iteration": iteration})
			return "", iteration, fmt.Errorf("LLM call failed: %w", err)
		}
		response := inferenceResult.Response
		messages = inferenceResult.MessagesUsed

		logger.InfoCF("agent", fmt.Sprintf("LLM iteration %d completed", iteration),
			map[string]any{
				"iteration":       iteration,
				"tool_call_count": len(response.ToolCalls),
				"content_length":  len(response.Content),
				"agent_id":        agent.ID,
				"session_key":     opts.SessionKey,
				"has_content":     response.Content != "",
				"has_tools":       len(response.ToolCalls) > 0,
			})

		// Check if no tool calls - we're done (or need continuation)
		if len(response.ToolCalls) == 0 {
			// Detect output truncation and attempt continuation before exiting.
			if response.FinishReason == "max_tokens" || response.FinishReason == "length" {
				logger.WarnCF("agent", "Output truncated by max_tokens, attempting continuation",
					map[string]any{
						"iteration":     iteration,
						"session_key":   opts.SessionKey,
						"content_len":   len(response.Content),
						"finish_reason": response.FinishReason,
					})
				finalContent = al.inference.ContinueAfterTruncation(ctx, agent, messages, response, opts)
				break
			}
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
					"second_last_role":            secondLastMsg.Role,
					"second_last_tool_call_count": len(secondLastMsg.ToolCalls),
					"last_role":                   lastMsg.Role,
					"last_tool_call_id":           lastMsg.ToolCallID,
					"last_content_preview":        utils.Truncate(lastMsg.Content, 100),
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
			"tool_call_count":      len(response.ToolCalls),
			"message_count_before": len(messages),
			"session_key":          opts.SessionKey,
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
	agent.SessionManager.AddToolResult(opts.SessionKey, assistantMsg)

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
		agent.Tools,
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
		if agent.SessionManager != nil {
			agent.SessionManager.AddToolResult(opts.SessionKey, toolResultMsg)
			logger.InfoCF("agent", "Saved tool result to session manager",
				map[string]any{
					"tool_call_id": toolResultMsg.ToolCallID,
					"content_len":  len(toolResultMsg.Content),
					"session_key":  opts.SessionKey,
				})
		}
	}

	// Verify that the session has been updated correctly
	if agent.SessionManager != nil {
		history := agent.SessionManager.GetHistory(opts.SessionKey)
		logger.InfoCF("agent", "Session history verified after tool results",
			map[string]any{
				"session_key":              opts.SessionKey,
				"history_count":            len(history),
				"last_msg_role":            history[len(history)-1].Role,
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
				"phase":     "tool_completed",
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
			err := al.memoryManager.SedimentKnowledgeWithType(
				toolResultSummary,
				fmt.Sprintf("tool_result_%s", tc.Name),
				memory.MemoryTypeToolObservation,
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

// createAssistantMessage creates an assistant message from the LLM response
func (al *AgentLoop) createAssistantMessage(response *providers.LLMResponse) providers.Message {
	assistantMsg := providers.Message{
		Role:    "assistant",
		Content: response.Content,
	}

	logger.InfoCF("agent", "Creating assistant message",
		map[string]any{
			"content_length":  len(response.Content),
			"tool_call_count": len(response.ToolCalls),
			"has_content":     response.Content != "",
			"has_tools":       len(response.ToolCalls) > 0,
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
				"tool_call_id":  toolCallEntry.ID,
				"tool_name":     toolCallEntry.Name,
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

	history := agent.SessionManager.GetHistory(sessionKey)
	summary := agent.SessionManager.GetSummary(sessionKey)

	// 获取相关的记忆上下文用于内存刷新
	var flushMemoryContext string
	if al.memoryManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel() // 确保取消上下文
		_ = ctx        // 显式使用ctx变量避免未使用警告

		// 使用之前的对话作为查询来获取相关记忆
		previousConversation := fmt.Sprintf("Previous conversation summary: %s, Last few exchanges: %v", summary, al.getLastExchanges(history, 3))
		searchResults, err := al.memoryManager.Search(previousConversation, 3) // 获取最多3个相关项
		if err != nil {
			logger.WarnCF("memory", "Failed to search memory context during flush", map[string]any{"error": err})
		} else if len(searchResults) > 0 {
			flushMemoryContext = formatMemoryContext(searchResults, 2)
		}
	}

	// Build a memory flush prompt that encourages the model to save important info
	memoryFlushPrompt := "Before we compact the conversation history, please save any important persistent information to your memory systems. " +
		"This is an automatic reminder to ensure no important data is lost during upcoming history compression. " +
		"You may reply with 'NO_REPLY' if no memory updates are needed."

	messages := al.contextBudget.BuildMessages(
		agent,
		history,
		summary,
		memoryFlushPrompt,
		flushMemoryContext,
		channel,
		chatID,
	)

	// Execute a silent call to encourage memory saving
	providerToolDefs := agent.Tools.ToProviderDefs()

	// Call LLM with memory flush prompt
	inferenceResult, err := al.inference.Invoke(ctx, agent, InferenceRequest{
		Messages:         messages,
		ProviderToolDefs: providerToolDefs,
		Options: processOptions{
			SessionKey:    sessionKey,
			Channel:       channel,
			ChatID:        chatID,
			UserMessage:   memoryFlushPrompt,
			MemoryContext: flushMemoryContext,
		},
	})

	if err != nil {
		logger.WarnCF("agent", "Memory flush failed", map[string]any{
			"error":       err.Error(),
			"session_key": sessionKey,
		})
		return
	}
	response := inferenceResult.Response

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
					"agent_id": agent.ID,
					"tool":     tc.Name,
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
			agent.SessionManager.AddFullMessage(sessionKey, toolResultMsg)

			// 新增：如果工具结果重要，则将其沉淀到记忆系统（内存刷新场景）
			if al.memoryManager != nil && contentForLLM != "" && shouldRememberToolResult(tc.Name) {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel() // 使用defer确保cancel被调用
					_ = ctx        // 显式使用ctx变量避免未使用警告
					// 创建一个描述工具调用和结果的摘要
					toolResultSummary := fmt.Sprintf("Memory flush tool '%s' executed with arguments %v, result: %s", tc.Name, tc.Arguments, contentForLLM)
					// 使用SedimentKnowledge方法沉淀工具执行结果
					err := al.memoryManager.SedimentKnowledgeWithType(
						toolResultSummary,
						fmt.Sprintf("memory_flush_tool_result_%s", tc.Name), // sourceKey
						memory.MemoryTypeToolObservation,
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
	newHistory := agent.SessionManager.GetHistory(sessionKey)
	if al.contextBudget.ShouldTriggerMemoryFlush(agent, newHistory) {
		go al.triggerMemoryFlush(agent, sessionKey, channel, chatID)
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
	history := agent.SessionManager.GetHistory(sessionKey)
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

	// Collapse dropped groups into compact summary messages instead of discarding them.
	// This retains a trace of past activity without the full token cost.
	startIdx := len(groupedMessages) - keepCount
	if startIdx < 0 {
		startIdx = 0
	}

	for i := 0; i < startIdx; i++ {
		if summary := collapseGroup(groupedMessages[i]); summary != "" {
			newHistory = append(newHistory, providers.Message{
				Role:    "system",
				Content: "[Collapsed] " + summary,
			})
		}
	}

	// Append the most recent groups in full.
	for i := startIdx; i < len(groupedMessages); i++ {
		newHistory = append(newHistory, groupedMessages[i]...)
	}

	// Update session with compressed history
	agent.SessionManager.SetHistory(sessionKey, newHistory)
	agent.SessionManager.Save(sessionKey)
}

// collapseGroup condenses a group of related messages into a single summary line.
// The result is used as a lightweight stand-in for the full message group when
// compressing history, preserving key information without the full token cost.
func collapseGroup(group []providers.Message) string {
	var parts []string
	for _, msg := range group {
		switch msg.Role {
		case "user":
			parts = append(parts, "User: "+utils.Truncate(msg.Content, 80))
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				names := make([]string, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					names = append(names, tc.Name)
				}
				parts = append(parts, "Called: ["+strings.Join(names, ", ")+"]")
			} else if msg.Content != "" {
				parts = append(parts, "Assistant: "+utils.Truncate(msg.Content, 80))
			}
		case "tool":
			id := msg.ToolCallID
			if len(id) > 8 {
				id = id[:8]
			}
			parts = append(parts, fmt.Sprintf("Tool(%s): %s", id, utils.Truncate(msg.Content, 60)))
		}
	}
	return strings.Join(parts, " → ")
}

// min is a helper function for integer minimum
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// shouldRememberToolResult determines if a tool result should be remembered in memory
func shouldRememberToolResult(toolName string) bool {
	// Define tools whose results should be remembered
	rememberTools := map[string]bool{
		"write_file":  true,
		"append_file": true,
		"mkdir":       true,
		"save_memory": true,
		"web_search":  true,
		"browser_get": true,
		"read_file":   true,
		"list_files":  true,
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

func formatMemoryContext(results []memory.SearchResult, limit int) string {
	if len(results) == 0 || limit <= 0 {
		return ""
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString("## Relevant Past Memories\n")

	for i, result := range results {
		if i >= limit {
			break
		}

		content := result.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}

		contextBuilder.WriteString(fmt.Sprintf(
			"### Memory %d: %s\nType: %s | Confidence: %.2f | Source: %s\n%s\n\n",
			i+1,
			result.Key,
			result.Type,
			result.Confidence,
			result.SourceKey,
			content,
		))
	}

	return contextBuilder.String()
}

// isContextOverflowError detects various forms of context overflow errors from different LLM providers
// IMPORTANT: It must NOT match Go standard library context errors (context deadline exceeded, context canceled)
// which indicate HTTP/request timeouts, not LLM context window issues.
func isContextOverflowError(errorStr string) bool {
	lowerError := strings.ToLower(errorStr)

	// First, exclude Go standard library context errors (these are timeout/cancellation errors)
	// "context deadline exceeded" - HTTP request or operation timeout
	// "context canceled" - operation was canceled
	if strings.Contains(lowerError, "context deadline exceeded") ||
		strings.Contains(lowerError, "context canceled") {
		return false
	}

	// Specific provider error messages for context overflow
	contextKeywords := []string{
		"context_length_exceeded",
		"context length exceeded",
		"max_tokens_exceeded",
		"max tokens exceeded",
		"token_limit_exceeded",
		"token limit exceeded",
		"input_too_long",
		"input too long",
		"request_too_large",
		"request too large",
		"model_input_too_long",
		"model input too long",
		"exceeds maximum",
		"exceeds context",
		"context window",
		"maximum context",
		"max context",
		"token limit",
		"length limit",
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
