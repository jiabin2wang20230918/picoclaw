package agent

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// ToolHandler handles all tool-related operations
type ToolHandler struct {
	config     *config.Config
	tools      *tools.ToolRegistry
	messageBus *bus.MessageBus
}


// NewToolHandler creates a new ToolHandler
func NewToolHandler(cfg *config.Config, tools *tools.ToolRegistry, messageBus *bus.MessageBus) *ToolHandler {
	return &ToolHandler{
		config:     cfg,
		tools:      tools,
		messageBus: messageBus,
	}
}

// ProcessToolCalls executes tool calls and returns tool result messages
func (th *ToolHandler) ProcessToolCalls(
	ctx context.Context,
	toolCalls []providers.ToolCall,
	channel, chatID string,
	progressCallback ProgressCallback,
	sessionKey string,
) ([]providers.Message, error) {
	var toolResultMessages []providers.Message

	// Normalize tool calls
	normalizedToolCalls := make([]providers.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		normalizedToolCalls = append(normalizedToolCalls, providers.NormalizeToolCall(tc))
	}

	// Execute each tool call
	for _, tc := range normalizedToolCalls {
		// Send progress notification for tool execution
		if th.config.Agents.Defaults.SendToolHints && progressCallback != nil {
			toolHint := fmt.Sprintf("Executing tool: %s", tc.Name)
			err := progressCallback(0, toolHint, map[string]interface{}{
				"tool_name":    tc.Name,
				"phase":        "tool_execution",
				"is_completed": false,
			})
			if err != nil {
				logger.WarnCF("agent", "Failed to send tool hint", map[string]any{"error": err})
			}
		}

		// Create async callback for tools that implement AsyncTool
		asyncCallback := func(callbackCtx context.Context, result *tools.ToolResult) {
			if !result.Silent && result.ForUser != "" {
				logger.InfoCF("agent", "Async tool completed, agent will handle notification",
					map[string]any{
						"tool":        tc.Name,
						"content_len": len(result.ForUser),
					})
			}
		}

		toolResult := th.tools.ExecuteWithContext(
			ctx,
			tc.Name,
			tc.Arguments,
			channel,
			chatID,
			asyncCallback,
		)

		// Determine content for LLM based on tool result (this is still needed for the LLM to continue processing)
		contentForLLM := toolResult.ForLLM
		if contentForLLM == "" && toolResult.Err != nil {
			contentForLLM = toolResult.Err.Error()
		}

		// Create tool result message for the LLM (this is necessary for the LLM to continue its reasoning)
		toolResultMsg := providers.Message{
			Role:       "tool",
			Content:    contentForLLM,
			ToolCallID: tc.ID,
		}

		toolResultMessages = append(toolResultMessages, toolResultMsg)
	}

	return toolResultMessages, nil
}


// shouldSendToolResultToUser determines if tool result should be sent to user
func (th *ToolHandler) shouldSendToolResultToUser(agent *AgentInstance, toolName string, result *tools.ToolResult) bool {
	// If tool is marked as silent, don't send to user
	if result.Silent {
		return false
	}

	// If SendToolHints is false, don't send any tool results to user either
	// This ensures that when tool hints are disabled, no tool-related messages are sent to the user
	if !th.config.Agents.Defaults.SendToolHints {
		return false
	}

	// Only send tool results that have meaningful content for the user
	return result.ForUser != "" && !result.Silent
}

