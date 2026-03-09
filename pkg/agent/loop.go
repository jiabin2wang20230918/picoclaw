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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	running        atomic.Bool
	summarizing    sync.Map
	fallback       *providers.FallbackChain
	channelManager *channels.Manager
	activeSessions sync.Map // tracks sessions currently being processed

	// Modular architecture components placeholder for future Beehive integration
	useModular     bool // Feature flag to control which system is used
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

	// Default to original system, can be overridden via config/env
	useModular := false // Use feature flag to enable modular system

	return &AgentLoop{
		bus:         msgBus,
		cfg:         cfg,
		registry:    registry,
		state:       stateManager,
		summarizing: sync.Map{},
		fallback:    fallbackChain,
		// Initialize modular architecture support but default to disabled
		useModular: useModular,
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
	if al.cfg.Agents.Defaults.SendProgress { // Use config instead of agent property
		initialProgressMsg := bus.OutboundMessage{
			Channel:     msg.Channel,
			ChatID:      msg.ChatID,
			Content:     "⏳ Received your request, starting to process...",
			MessageType: bus.MessageTypeProgress,
			Progress:    &[]float64{0.0}[0], // 0% at start
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
		history = agent.Sessions.GetHistory(opts.SessionKey)
		summary = agent.Sessions.GetSummary(opts.SessionKey)
	}
	messages := agent.ContextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		nil,
		opts.Channel,
		opts.ChatID,
	)

	// 3. Save user message to session
	agent.Sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)

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
		// Check if recent tool activity occurred in the last few messages
		history := agent.Sessions.GetHistory(opts.SessionKey)
		if hasRecentToolActivity(history, 5) { // Check recent 5 messages for tool activity
			// If recent tool activity exists, don't show default message (silent completion)
			finalContent = ""
		} else {
			// Otherwise, use the default response
			finalContent = opts.DefaultResponse
		}
	}

	// 6. Save final assistant message to session
	agent.Sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
	agent.Sessions.Save(opts.SessionKey)

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
func (al *AgentLoop) runLLMIteration(
	ctx context.Context,
	agent *AgentInstance,
	messages []providers.Message,
	opts processOptions,
	progressCallback ProgressCallback,
) (string, int, error) {
	iteration := 0
	var finalContent string

	for iteration < agent.MaxIterations {
		iteration++

		// Send progress update at the beginning of each iteration
		if progressCallback != nil {
			progress := float64(iteration) / float64(agent.MaxIterations) * 100.0
			err := progressCallback(progress, fmt.Sprintf("Processing iteration %d/%d", iteration, agent.MaxIterations), map[string]interface{}{
				"iteration":     iteration,
				"max_iter":      agent.MaxIterations,
				"phase":         "llm_processing",
				"is_completed":  false,
			})
			if err != nil {
				logger.WarnCF("agent", "Failed to send progress update", map[string]any{"error": err})
			}
		}

		// Build tool definitions
		providerToolDefs := agent.Tools.ToProviderDefs()

		// Log LLM request details
		logger.DebugCF("agent", "LLM request",
			map[string]any{
				"agent_id":          agent.ID,
				"iteration":         iteration,
				"model":             agent.Model,
				"messages_count":    len(messages),
				"tools_count":       len(providerToolDefs),
				"max_tokens":        agent.MaxTokens,
				"temperature":       agent.Temperature,
				"system_prompt_len": len(messages[0].Content),
			})

		// Log full messages (detailed)
		logger.DebugCF("agent", "Full LLM request",
			map[string]any{
				"iteration":     iteration,
				"messages_json": formatMessagesForLog(messages),
				"tools_json":    formatToolsForLog(providerToolDefs),
			})

		// Apply session pruning to reduce memory usage by trimming old tool results
		// Only if the agent has compaction config available and pruning is configured
		if agent.CompactionConfig.KeepRecentTokens > 0 {
			messages = al.pruneSessionMemory(agent, opts.SessionKey, messages)
		}

		// Call LLM with fallback chain if candidates are configured.
		var response *providers.LLMResponse
		var err error

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
						map[string]any{"agent_id": agent.ID, "iteration": iteration})
				}
				return fbResult.Response, nil
			}
			return agent.Provider.Chat(ctx, messages, providerToolDefs, agent.Model, map[string]any{
				"max_tokens":  agent.MaxTokens,
				"temperature": agent.Temperature,
			})
		}

		// Retry loop for context/token errors
		maxRetries := 2
		for retry := 0; retry <= maxRetries; retry++ {
			response, err = callLLM()
			if err == nil {
				break
			}

			if isContextOverflowError(err.Error()) && retry < maxRetries {
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

				al.forceCompression(agent, opts.SessionKey)
				newHistory := agent.Sessions.GetHistory(opts.SessionKey)
				newSummary := agent.Sessions.GetSummary(opts.SessionKey)
				// IMPORTANT: Include the original user message when rebuilding messages after compression
				messages = agent.ContextBuilder.BuildMessages(
					newHistory, newSummary, opts.UserMessage,
					nil, opts.Channel, opts.ChatID,
				)
				continue
			}
			break
		}

		if err != nil {
			logger.ErrorCF("agent", "LLM call failed",
				map[string]any{
					"agent_id":  agent.ID,
					"iteration": iteration,
					"error":     err.Error(),
				})
			return "", iteration, fmt.Errorf("LLM call failed after retries: %w", err)
		}

		// Check if no tool calls - we're done
		if len(response.ToolCalls) == 0 {
			finalContent = response.Content
			logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
				map[string]any{
					"agent_id":      agent.ID,
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})
			break
		}

		normalizedToolCalls := make([]providers.ToolCall, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			normalizedToolCalls = append(normalizedToolCalls, providers.NormalizeToolCall(tc))
		}

		// Send response content as progress feedback if present and enabled
		if response.Content != "" && al.cfg.Agents.Defaults.SendProgress && progressCallback != nil {
			// Extract and send thinking content (similar to nanobot's _strip_think approach)
			// Remove any potential thinking blocks that might be embedded in content
			thoughtContent := strings.TrimSpace(response.Content)

			// Only send if content seems like thinking/intermediate result rather than empty/boilerplate
			if len(thoughtContent) > 0 {
				currentProgress := float64(iteration-1) / float64(agent.MaxIterations) * 100.0
				err := progressCallback(currentProgress, fmt.Sprintf("💭 Thinking: %s", utils.Truncate(thoughtContent, 100)), map[string]interface{}{
					"phase":     "llm_thinking",
					"iteration": iteration,
					"has_tools": len(normalizedToolCalls) > 0,
				})
				if err != nil {
					logger.WarnCF("agent", "Failed to send thinking progress", map[string]any{"error": err})
				}
			}
		}

		// Log tool calls
		toolNames := make([]string, 0, len(normalizedToolCalls))
		for _, tc := range normalizedToolCalls {
			toolNames = append(toolNames, tc.Name)
		}
		logger.InfoCF("agent", "LLM requested tool calls",
			map[string]any{
				"agent_id":  agent.ID,
				"tools":     toolNames,
				"count":     len(normalizedToolCalls),
				"iteration": iteration,
			})

		// Build assistant message with tool calls
		assistantMsg := providers.Message{
			Role:    "assistant",
			Content: response.Content,
		}
		for _, tc := range normalizedToolCalls {
			argumentsJSON, _ := json.Marshal(tc.Arguments)
			// Copy ExtraContent to ensure thought_signature is persisted for Gemini 3
			extraContent := tc.ExtraContent
			thoughtSignature := ""
			if tc.Function != nil {
				thoughtSignature = tc.Function.ThoughtSignature
			}

			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
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
			})
		}
		messages = append(messages, assistantMsg)

		// Save assistant message with tool calls to session
		agent.Sessions.AddFullMessage(opts.SessionKey, assistantMsg)

		// Execute tool calls with steering check
		var steeringMsg *bus.SteeringMessage
		for i, tc := range normalizedToolCalls {
			// Check for steering message before each tool
			if steering, ok := al.bus.ConsumeSteeringForSession(opts.SessionKey); ok {
				steeringMsg = &steering
				logger.InfoCF("agent", "Steering message received, skipping remaining tools",
					map[string]any{
						"agent_id":     agent.ID,
						"session_key":  opts.SessionKey,
						"skipped_from": i,
						"total_tools":  len(normalizedToolCalls),
					})
				// Skip remaining tools
				for _, skipTC := range normalizedToolCalls[i:] {
					skipResult := skipToolCall(skipTC)
					messages = append(messages, skipResult)
					agent.Sessions.AddFullMessage(opts.SessionKey, skipResult)
				}
				break
			}

			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]any{
					"agent_id":  agent.ID,
					"tool":      tc.Name,
					"iteration": iteration,
				})

			// Send tool hint if enabled and it's not a silent tool
			if al.cfg.Agents.Defaults.SendToolHints && progressCallback != nil {
				toolHint := fmt.Sprintf("🔧 Executing tool: %s", tc.Name)
				// Calculate more granular progress considering tool execution within iteration
				// Tools executed mid-iteration, so reflect their execution in the progress
				currentProgress := float64(iteration-1) / float64(agent.MaxIterations) * 100.0
				// Add fractional progress for tools within current iteration
				toolIndex := float64(i) // Current tool index in the iteration
				toolCount := float64(len(normalizedToolCalls))
				if toolCount > 0 {
					fractionalProgress := toolIndex / toolCount * (100.0 / float64(agent.MaxIterations))
					currentProgress += fractionalProgress
				}

				err := progressCallback(currentProgress, toolHint, map[string]interface{}{
					"tool_name":     tc.Name,
					"phase":         "tool_execution",
					"iteration":     iteration,
					"is_completed":  false,
				})
				if err != nil {
					logger.WarnCF("agent", "Failed to send tool hint", map[string]any{"error": err})
				}
			}

			// Create async callback for tools that implement AsyncTool
			// NOTE: Following openclaw's design, async tools do NOT send results directly to users.
			// Instead, they notify the agent via PublishInbound, and the agent decides
			// whether to forward the result to the user (in processSystemMessage).
			asyncCallback := func(callbackCtx context.Context, result *tools.ToolResult) {
				// Log the async completion but don't send directly to user
				// The agent will handle user notification via processSystemMessage
				if !result.Silent && result.ForUser != "" {
					logger.InfoCF("agent", "Async tool completed, agent will handle notification",
						map[string]any{
							"tool":        tc.Name,
							"content_len": len(result.ForUser),
						})
				}
			}

			toolResult := agent.Tools.ExecuteWithContext(
				ctx,
				tc.Name,
				tc.Arguments,
				opts.Channel,
				opts.ChatID,
				asyncCallback,
			)

			// Send tool result progress notification if enabled
			if al.cfg.Agents.Defaults.SendToolHints && progressCallback != nil {
				toolResultHint := fmt.Sprintf("✅ Tool '%s' completed", tc.Name)
				// Calculate more granular progress considering tool execution within iteration
				currentProgress := float64(iteration-1) / float64(agent.MaxIterations) * 100.0
				// Add fractional progress for tools within current iteration
				toolIndex := float64(i+1) // Use i+1 since the tool is now completed
				toolCount := float64(len(normalizedToolCalls))
				if toolCount > 0 {
					fractionalProgress := toolIndex / toolCount * (100.0 / float64(agent.MaxIterations))
					currentProgress += fractionalProgress
				}

				err := progressCallback(currentProgress, toolResultHint, map[string]interface{}{
					"tool_name":     tc.Name,
					"phase":         "tool_completed",
					"iteration":     iteration,
					"is_completed":  true,
				})
				if err != nil {
					logger.WarnCF("agent", "Failed to send tool result hint", map[string]any{"error": err})
				}
			}

			// NEW: Determine if tool result should be sent to user based on system configuration,
			// not just on tool-level Silent flag. This moves the decision logic to system level.
			shouldSendToUser := al.shouldSendToolResultToUser(agent, tc.Name, toolResult)

			// Send tool result progress notification if enabled
			if al.cfg.Agents.Defaults.SendToolHints && progressCallback != nil {
				var toolResultHint string
				if shouldSendToUser && toolResult.ForUser != "" {
					toolResultHint = fmt.Sprintf("✅ Tool '%s' completed: %s", tc.Name, toolResult.ForUser[:min(len(toolResult.ForUser), 50)])
					if len(toolResult.ForUser) > 50 {
						toolResultHint += "..."
					}
				} else {
					toolResultHint = fmt.Sprintf("✅ Tool '%s' completed (result not shown to user)", tc.Name)
				}

				// Calculate more granular progress considering tool execution within iteration
				currentProgress := float64(iteration-1) / float64(agent.MaxIterations) * 100.0
				// Add fractional progress for tools within current iteration
				toolIndex := float64(i+1) // Use i+1 since the tool is now completed
				toolCount := float64(len(normalizedToolCalls))
				if toolCount > 0 {
					fractionalProgress := toolIndex / toolCount * (100.0 / float64(agent.MaxIterations))
					currentProgress += fractionalProgress
				}

				err := progressCallback(currentProgress, toolResultHint, map[string]interface{}{
					"tool_name":     tc.Name,
					"tool_args":     tc.Arguments,
					"tool_result_len": len(toolResult.ForLLM),
					"sent_to_user":  shouldSendToUser,
					"iteration":     iteration,
					"phase":         "tool_completed",
					"is_completed":  true,
				})
				logger.DebugCF("agent", "Published tool result hint", map[string]any{
					"tool":          tc.Name,
					"progress":      currentProgress,
					"hint":          toolResultHint,
					"sent_to_user":  shouldSendToUser,
					"iteration":     iteration,
					"phase":         "tool_completed",
					"is_completed":  true,
				})
				if err != nil {
					logger.WarnCF("agent", "Failed to send tool result hint", map[string]any{"error": err})
				}
			}

			// NEW: Send ForUser content to user based on system-level configuration
			// rather than tool-level silencing alone
			if shouldSendToUser && toolResult.ForUser != "" {
				// Send as regular message (existing behavior)
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.ChatID,
					Content: toolResult.ForUser,
				})

				// ALSO send as progress message for intermediate results display
				progressContent := fmt.Sprintf("🔧 Tool '%s' completed: %s", tc.Name,
					utils.Truncate(toolResult.ForUser, 100)) // Truncate for progress display
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel:     opts.Channel,
					ChatID:      opts.ChatID,
					Content:     progressContent,
					MessageType: bus.MessageTypeProgress,
				})

				logger.DebugCF("agent", "Sent tool result to user",
					map[string]any{
						"tool":        tc.Name,
						"content_len": len(toolResult.ForUser),
					})
			}

			// Additionally, if progress callback is available, send intermediate tool result
			if al.cfg.Agents.Defaults.SendToolHints && progressCallback != nil && toolResult.ForUser != "" {
				intermediateMsg := fmt.Sprintf("🔄 Running tool: %s - %s", tc.Name, utils.Truncate(toolResult.ForUser, 50))
				_ = progressCallback(float64(iteration-1)/float64(agent.MaxIterations)*100.0, intermediateMsg, map[string]interface{}{
					"tool_name":    tc.Name,
					"phase":        "tool_execution_intermediate",
					"iteration":    iteration,
				})
			}

			// Determine content for LLM based on tool result
			contentForLLM := toolResult.ForLLM
			if contentForLLM == "" && toolResult.Err != nil {
				contentForLLM = toolResult.Err.Error()
			}

			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolResultMsg)

			// Save tool result message to session
			agent.Sessions.AddFullMessage(opts.SessionKey, toolResultMsg)
		}

		// If steering was received, inject user message and continue
		if steeringMsg != nil {
			userMsg := providers.Message{
				Role:    "user",
				Content: fmt.Sprintf("[User interrupted]: %s", steeringMsg.Content),
			}
			messages = append(messages, userMsg)
			agent.Sessions.AddMessage(opts.SessionKey, "user", userMsg.Content)
			logger.InfoCF("agent", "Steering message injected into conversation",
				map[string]any{
					"session_key": opts.SessionKey,
					"content":     utils.Truncate(steeringMsg.Content, 50),
				})
			// Continue to next iteration to process the steering message
			continue
		}
	}

	// Check if we've reached the maximum iterations without completion
	if iteration >= agent.MaxIterations && finalContent == "" {
		// Send a final progress update indicating max iterations reached
		if progressCallback != nil {
			err := progressCallback(100.0, fmt.Sprintf("Max iterations reached (%d). Need further input to continue.", agent.MaxIterations), map[string]interface{}{
				"iteration":     iteration,
				"max_iter":      agent.MaxIterations,
				"phase":         "max_iterations_reached",
				"is_completed":  false,
			})
			if err != nil {
				logger.WarnCF("agent", "Failed to send max iterations progress update", map[string]any{"error": err})
			}
		}

		// Send a completion notification that indicates the agent has paused due to max iterations
		al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: fmt.Sprintf("I've reached the maximum number of processing iterations (%d). I may need additional input to continue. Would you like me to continue working on this task?", agent.MaxIterations),
		})
	}

	return finalContent, iteration, nil
}

// skipToolCall creates a tool result message for skipped calls due to user interruption.
func skipToolCall(tc providers.ToolCall) providers.Message {
	return providers.Message{
		Role:       "tool",
		Content:    "Skipped due to user interruption.",
		ToolCallID: tc.ID,
	}
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

	// Build a memory flush prompt that encourages the model to save important info
	memoryFlushPrompt := "Before we compact the conversation history, please save any important persistent information to your memory systems. " +
		"This is an automatic reminder to ensure no important data is lost during upcoming history compression. " +
		"You may reply with 'NO_REPLY' if no memory updates are needed."

	messages := agent.ContextBuilder.BuildMessages(
		history,
		summary,
		memoryFlushPrompt,
		nil,
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
	keepCount := len(groupedMessages) / 2 // Keep half of the message groups
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
		baseContent = contentParts[0]

		// Add compression note to the original system prompt
		droppedCount := len(history) - keepCount // Approximation
		if len(contentParts) > 1 {
			// There's already a compression note, add to it
			droppedCount = al.parseDroppedCount(contentParts[1]) + (len(history) - keepCount) // Combine dropped counts
		}

		compressionNote := fmt.Sprintf(
			"\n\n[System Note: Emergency compression dropped %d oldest messages due to context limit]",
			droppedCount,
		)
		enhancedSystemPrompt := history[0]
		enhancedSystemPrompt.Content = baseContent + compressionNote
		newHistory = append(newHistory, enhancedSystemPrompt)
	} else if len(history) > 0 {
		// If first message is not system, include it
		newHistory = append(newHistory, history[0])
	}

	// Add the most recent message groups
	startIdx := len(groupedMessages) - keepCount
	if startIdx < 0 {
		startIdx = 0
	}

	for i := startIdx; i < len(groupedMessages); i++ {
		newHistory = append(newHistory, groupedMessages[i]...)
	}

	// Always ensure we end with the last message if it's different from our content
	if len(history) > 0 && len(newHistory) > 0 &&
		(len(newHistory) == 1 || newHistory[len(newHistory)-1].Content != history[len(history)-1].Content) {
		// Add last message if it's not already included
		newHistory = append(newHistory, history[len(history)-1])
	}

	// Update session
	agent.Sessions.SetHistory(sessionKey, newHistory)
	agent.Sessions.Save(sessionKey)

	logger.WarnCF("agent", "Forced compression executed", map[string]any{
		"session_key":  sessionKey,
		"dropped_msgs": len(history) - len(newHistory),
		"new_count":    len(newHistory),
	})
}

// groupRelatedMessages groups related messages together, particularly preserving
// assistant messages with tool_calls and their corresponding tool result messages
func (al *AgentLoop) groupRelatedMessages(messages []providers.Message) [][]providers.Message {
	if len(messages) == 0 {
		return [][]providers.Message{}
	}

	var groups [][]providers.Message
	var currentGroup []providers.Message

	for i, msg := range messages {
		// Start a new group if this is a system message or if we're at the start
		if msg.Role == "system" || len(currentGroup) == 0 {
			if len(currentGroup) > 0 {
				groups = append(groups, currentGroup)
			}
			currentGroup = []providers.Message{msg}
		} else if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			// Assistant message with tool calls - start a new group to capture the result
			if len(currentGroup) > 0 {
				groups = append(groups, currentGroup)
			}
			currentGroup = []providers.Message{msg}
		} else if msg.Role == "tool" {
			// Tool result message - add to current group if previous message was assistant with tool call
			if len(currentGroup) > 0 {
				// Check if there was a preceding assistant message with tool calls that this result corresponds to
				// Look for matching ToolCallID in previous assistant message's ToolCalls
				needsGrouping := false
				for _, prevMsg := range currentGroup {
					if prevMsg.Role == "assistant" && len(prevMsg.ToolCalls) > 0 {
						// Found an assistant message with tool calls in current group
						needsGrouping = true
						break
					}
				}

				// If current group has an assistant with tool calls, or if previous message was an assistant with tool calls
				if needsGrouping || (i > 0 && messages[i-1].Role == "assistant" && len(messages[i-1].ToolCalls) > 0) {
					currentGroup = append(currentGroup, msg)
				} else {
					// Standalone tool message - create new group
					if len(currentGroup) > 0 {
						groups = append(groups, currentGroup)
					}
					currentGroup = []providers.Message{msg}
				}
			} else {
				// Standalone tool message - shouldn't normally happen but just in case
				currentGroup = []providers.Message{msg}
			}
		} else {
			// Regular message (user, assistant without tools) - if previous was a complete tool interaction, start new group
			if len(currentGroup) > 0 {
				groups = append(groups, currentGroup)
			}
			currentGroup = []providers.Message{msg}
		}
	}

	// Add the last group
	if len(currentGroup) > 0 {
		groups = append(groups, currentGroup)
	}

	return groups
}

// parseDroppedCount extracts the dropped message count from a compression note string
func (al *AgentLoop) parseDroppedCount(note string) int {
	// Look for pattern "dropped X oldest messages" in the note
	parts := strings.Split(note, "dropped ")
	if len(parts) > 1 {
		nextParts := strings.Split(parts[1], " ")
		if len(nextParts) > 0 {
			if count, err := strconv.Atoi(nextParts[0]); err == nil {
				return count
			}
		}
	}
	return 0
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

// formatMessagesForLog formats messages for logging
func formatMessagesForLog(messages []providers.Message) string {
	if len(messages) == 0 {
		return "[]"
	}

	var sb strings.Builder
	sb.WriteString("[\n")
	for i, msg := range messages {
		fmt.Fprintf(&sb, "  [%d] Role: %s\n", i, msg.Role)
		if len(msg.ToolCalls) > 0 {
			sb.WriteString("  ToolCalls:\n")
			for _, tc := range msg.ToolCalls {
				fmt.Fprintf(&sb, "    - ID: %s, Type: %s, Name: %s\n", tc.ID, tc.Type, tc.Name)
				if tc.Function != nil {
					fmt.Fprintf(&sb, "      Arguments: %s\n", utils.Truncate(tc.Function.Arguments, 200))
				}
			}
		}
		if msg.Content != "" {
			content := utils.Truncate(msg.Content, 200)
			fmt.Fprintf(&sb, "  Content: %s\n", content)
		}
		if msg.ToolCallID != "" {
			fmt.Fprintf(&sb, "  ToolCallID: %s\n", msg.ToolCallID)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("]")
	return sb.String()
}

// formatToolsForLog formats tool definitions for logging
func formatToolsForLog(toolDefs []providers.ToolDefinition) string {
	if len(toolDefs) == 0 {
		return "[]"
	}

	var sb strings.Builder
	sb.WriteString("[\n")
	for i, tool := range toolDefs {
		fmt.Fprintf(&sb, "  [%d] Type: %s, Name: %s\n", i, tool.Type, tool.Function.Name)
		fmt.Fprintf(&sb, "      Description: %s\n", tool.Function.Description)
		if len(tool.Function.Parameters) > 0 {
			fmt.Fprintf(&sb, "      Parameters: %s\n", utils.Truncate(fmt.Sprintf("%v", tool.Function.Parameters), 200))
		}
	}
	sb.WriteString("]")
	return sb.String()
}

// summarizeSession summarizes the conversation history for a session.
func (al *AgentLoop) summarizeSession(agent *AgentInstance, sessionKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistory(sessionKey)
	summary := agent.Sessions.GetSummary(sessionKey)

	// Keep last 4 messages for continuity
	if len(history) <= 4 {
		return
	}

	toSummarize := history[:len(history)-4]

	// Use agent's compaction config for smarter token management
	reserveTokens := agent.CompactionConfig.ReserveTokens
	if reserveTokens == 0 {
		reserveTokens = 16384 // default
	}

	// Oversized Message Guard - use compaction config if available
	maxMessageTokens := reserveTokens / 2
	if agent.CompactionConfig.ReserveTokens > 0 {
		maxMessageTokens = agent.CompactionConfig.ReserveTokens / 2
	}

	// Smart message filtering with priority handling
	validMessages := make([]providers.Message, 0)
	omitted := false

	for _, m := range toSummarize {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}

		// Calculate tokens using our estimator
		msgTokens := al.estimateTokens([]providers.Message{m})
		if msgTokens > maxMessageTokens {
			omitted = true
			continue
		}

		// Include the message in valid messages
		validMessages = append(validMessages, m)
	}

	if len(validMessages) == 0 {
		return
	}

	// Multi-Part Summarization with configurable chunk size
	var finalSummary string

	// Use compaction config for chunk sizing, default to 10 if not set
	chunkSize := 10
	if agent.CompactionConfig.KeepRecentTokens > 10000 { // arbitrary threshold for determining if it's set
		// Derive chunk size based on available space
		chunkSize = 15  // Increase default for better performance with smart config
	}

	if len(validMessages) > chunkSize {
		mid := len(validMessages) / 2
		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		s1, _ := al.summarizeBatch(ctx, agent, part1, "")
		s2, _ := al.summarizeBatch(ctx, agent, part2, "")

		mergePrompt := fmt.Sprintf(
			"Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s",
			s1,
			s2,
		)
		resp, err := agent.Provider.Chat(
			ctx,
			[]providers.Message{{Role: "user", Content: mergePrompt}},
			nil,
			agent.Model,
			map[string]any{
				"max_tokens":  1024,
				"temperature": 0.3,
			},
		)
		if err == nil {
			finalSummary = resp.Content
		} else {
			finalSummary = s1 + " " + s2
		}
	} else {
		finalSummary, _ = al.summarizeBatch(ctx, agent, validMessages, summary)
	}

	if omitted && finalSummary != "" {
		finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
	}

	if finalSummary != "" {
		agent.Sessions.SetSummary(sessionKey, finalSummary)
		agent.Sessions.TruncateHistory(sessionKey, 4)
		agent.Sessions.Save(sessionKey)
	}
}

// summarizeBatch summarizes a batch of messages.
func (al *AgentLoop) summarizeBatch(
	ctx context.Context,
	agent *AgentInstance,
	batch []providers.Message,
	existingSummary string,
) (string, error) {
	var sb strings.Builder
	sb.WriteString("Provide a concise summary of this conversation segment, preserving core context and key points.\n")
	if existingSummary != "" {
		sb.WriteString("Existing context: ")
		sb.WriteString(existingSummary)
		sb.WriteString("\n")
	}
	sb.WriteString("\nCONVERSATION:\n")
	for _, m := range batch {
		fmt.Fprintf(&sb, "%s: %s\n", m.Role, m.Content)
	}
	prompt := sb.String()

	response, err := agent.Provider.Chat(
		ctx,
		[]providers.Message{{Role: "user", Content: prompt}},
		nil,
		agent.Model,
		map[string]any{
			"max_tokens":  1024,
			"temperature": 0.3,
		},
	)
	if err != nil {
		return "", err
	}
	return response.Content, nil
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
	args := parts[1:]

	switch cmd {
	case "/show":
		if len(args) < 1 {
			return "Usage: /show [model|channel|agents]", true
		}
		switch args[0] {
		case "model":
			defaultAgent := al.registry.GetDefaultAgent()
			if defaultAgent == nil {
				return "No default agent configured", true
			}
			return fmt.Sprintf("Current model: %s", defaultAgent.Model), true
		case "channel":
			return fmt.Sprintf("Current channel: %s", msg.Channel), true
		case "agents":
			agentIDs := al.registry.ListAgentIDs()
			return fmt.Sprintf("Registered agents: %s", strings.Join(agentIDs, ", ")), true
		default:
			return fmt.Sprintf("Unknown show target: %s", args[0]), true
		}

	case "/list":
		if len(args) < 1 {
			return "Usage: /list [models|channels|agents]", true
		}
		switch args[0] {
		case "models":
			return "Available models: configured in config.json per agent", true
		case "channels":
			if al.channelManager == nil {
				return "Channel manager not initialized", true
			}
			channels := al.channelManager.GetEnabledChannels()
			if len(channels) == 0 {
				return "No channels enabled", true
			}
			return fmt.Sprintf("Enabled channels: %s", strings.Join(channels, ", ")), true
		case "agents":
			agentIDs := al.registry.ListAgentIDs()
			return fmt.Sprintf("Registered agents: %s", strings.Join(agentIDs, ", ")), true
		default:
			return fmt.Sprintf("Unknown list target: %s", args[0]), true
		}

	case "/switch":
		if len(args) < 3 || args[1] != "to" {
			return "Usage: /switch [model|channel] to <name>", true
		}
		target := args[0]
		value := args[2]

		switch target {
		case "model":
			defaultAgent := al.registry.GetDefaultAgent()
			if defaultAgent == nil {
				return "No default agent configured", true
			}
			oldModel := defaultAgent.Model
			defaultAgent.Model = value
			return fmt.Sprintf("Switched model from %s to %s", oldModel, value), true
		case "channel":
			if al.channelManager == nil {
				return "Channel manager not initialized", true
			}
			if _, exists := al.channelManager.GetChannel(value); !exists && value != "cli" {
				return fmt.Sprintf("Channel '%s' not found or not enabled", value), true
			}
			return fmt.Sprintf("Switched target channel to %s", value), true
		default:
			return fmt.Sprintf("Unknown switch target: %s", target), true
		}

	case "/interrupt":
		if len(args) == 0 {
			return "Usage: /interrupt <message>", true
		}
		content := strings.Join(args, " ")
		sessionKey := al.determineSessionKey(msg)
		al.bus.PublishSteering(bus.SteeringMessage{
			Channel:    msg.Channel,
			ChatID:     msg.ChatID,
			Content:    content,
			SessionKey: sessionKey,
			Timestamp:  time.Now().Unix(),
		})
		return "Interrupt signal sent.", true

	case "/compact":
		sessionKey := al.determineSessionKey(msg)
		go func() {
			// Optionally use any additional arguments as compaction instructions
			// (Currently we use them to customize the summary approach, but the core summarizeSession handles the instruction)

			// Override the session summary by running a manual summarization
			defaultAgent := al.registry.GetDefaultAgent()
			if defaultAgent != nil {
				// Force a compaction by calling summarizeSession directly
				summarizeKey := defaultAgent.ID + ":" + sessionKey
				if _, loading := al.summarizing.LoadOrStore(summarizeKey, true); !loading {
					defer al.summarizing.Delete(summarizeKey)

					if !constants.IsInternalChannel(msg.Channel) {
						al.bus.PublishOutbound(bus.OutboundMessage{
							Channel: msg.Channel,
							ChatID:  msg.ChatID,
							Content: "Manual compaction initiated. Optimizing conversation history...",
						})
					}
					al.summarizeSession(defaultAgent, sessionKey)
				}
			}
		}()
		return "Compaction started in background.", true

	case "/new":
		if len(args) < 1 {
			return "Usage: /new [session]", true
		}
		switch args[0] {
		case "session":
			sessionKey := al.determineSessionKey(msg)
			// Clear the session history to start fresh
			defaultAgent := al.registry.GetDefaultAgent()
			if defaultAgent != nil {
				// Reset the session by truncating its history to 0 messages
				defaultAgent.Sessions.TruncateHistory(sessionKey, 0)
				// Also clear the summary
				defaultAgent.Sessions.SetSummary(sessionKey, "")
				// Save the cleared session
				defaultAgent.Sessions.Save(sessionKey)
				return "New session started. Previous conversation history cleared.", true
			}
			return "No default agent configured", true
		default:
			return fmt.Sprintf("Unknown new command: %s. Available: /new session", args[0]), true
		}
	}

	return "", false
}

// extractPeer extracts the routing peer from inbound message metadata.
func extractPeer(msg bus.InboundMessage) *routing.RoutePeer {
	peerKind := msg.Metadata["peer_kind"]
	if peerKind == "" {
		return nil
	}
	peerID := msg.Metadata["peer_id"]
	if peerID == "" {
		if peerKind == "direct" {
			peerID = msg.SenderID
		} else {
			peerID = msg.ChatID
		}
	}
	return &routing.RoutePeer{Kind: peerKind, ID: peerID}
}

// extractParentPeer extracts the parent peer (reply-to) from inbound message metadata.
func extractParentPeer(msg bus.InboundMessage) *routing.RoutePeer {
	parentKind := msg.Metadata["parent_peer_kind"]
	parentID := msg.Metadata["parent_peer_id"]
	if parentKind == "" || parentID == "" {
		return nil
	}
	return &routing.RoutePeer{Kind: parentKind, ID: parentID}
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

// isContextOverflowError detects various forms of context overflow errors from different LLM providers
func isContextOverflowError(errorStr string) bool {
	lowerError := strings.ToLower(errorStr)

	// Basic token/context related errors
	if strings.Contains(lowerError, "token") ||
	   strings.Contains(lowerError, "context") ||
	   strings.Contains(lowerError, "length") {
		return true
	}

	// Specific error patterns from various providers
	patterns := []string{
		"request_too_large",
		"request exceeds the maximum size",
		"maximum context length",
		"prompt is too long:",
		"context overflow:",
		"413 request entity too large",
		"request size exceeds model context window",
		"exceeds model token limit",
		"input length and max_tokens exceed context limit",
		"this request exceeds the model's maximum context length",
		"llm request rejected: max_tokens would exceed context window",
		"input length would exceed context budget for this model",
		"上下文过长",
		"错误：上下文过长，请减少输入",
		"上下文超出限制",
		"上下文长度超出模型最大限制",
		"超出最大上下文长度",
		"请压缩上下文后重试",
		"invalidparameter",
	}

	for _, pattern := range patterns {
		if strings.Contains(lowerError, strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

// hasRecentToolActivity checks if recent messages contain tool activity
func hasRecentToolActivity(history []providers.Message, recentCount int) bool {
	// Get the most recent messages
	startIdx := len(history) - recentCount
	if startIdx < 0 {
		startIdx = 0
	}

	// Check recent messages for tool activity
	for i := len(history) - 1; i >= startIdx && i >= 0; i-- {
		msg := history[i]
		// Look for assistant messages that triggered tools
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			return true
		}
		// Look for tool result messages
		if msg.Role == "tool" {
			return true
		}
	}
	return false
}

// shouldSendToolResultToUser determines if a tool result should be sent to the user
// based on agent configuration and tool result characteristics, moving the decision
// from tool-level to system-level control
func (al *AgentLoop) shouldSendToolResultToUser(agent *AgentInstance, toolName string, result *tools.ToolResult) bool {
	// If explicitly silenced by tool, respect that (for backward compatibility)
	if result.Silent {
		return false
	}

	// If there's no content for user, don't send anything
	if result.ForUser == "" {
		return false
	}

	// NEW LOGIC: Use agent configuration to determine tool result visibility policy
	// This centralizes the decision-making process in the system rather than letting each tool decide

	// Check agent's tool hints configuration (this is the system-level control)
	// If SendToolHints is disabled, don't show results to user
	if !al.cfg.Agents.Defaults.SendToolHints {
		return false
	}

	// If SendToolHints is enabled, then allow results to be sent to user
	// (Additional filtering could be implemented here based on tool types or other policies)

	return true
}
