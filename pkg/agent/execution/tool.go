// Package execution handles tool execution and result management.
package execution

import (
	"context"
	"encoding/json"

	"github.com/sipeed/picoclaw/pkg/agent/interfaces"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// ToolExecutor executes tools and manages results.
type ToolExecutor struct {
	msgBus *bus.MessageBus
}

// NewToolExecutor creates a new tool executor instance.
func NewToolExecutor(msgBus *bus.MessageBus) *ToolExecutor {
	return &ToolExecutor{
		msgBus: msgBus,
	}
}

// ExecuteTools executes the requested tools and returns the results.
func (te *ToolExecutor) ExecuteTools(ctx context.Context, agent *interfaces.AgentInstance, request *interfaces.ToolRequest) (*interfaces.ToolResult, error) {
	var results []providers.Message
	var forUserContent string

	// Since ToolRequest doesn't have channel/chatID info, we'll need to get it elsewhere
	// For now, using empty strings as placeholders
	channel := ""  // Placeholder - needs to come from somewhere else
	chatID := ""   // Placeholder - needs to come from somewhere else
	sendResponse := true // Placeholder

	for _, tc := range request.ToolCalls {
		// Execute the tool
		argsJSON, _ := json.Marshal(tc.Arguments)
		argsPreview := string(argsJSON)[:min(len(argsJSON), 200)]
		logger.InfoCF("agent", "Executing tool: "+tc.Name+"("+argsPreview+")",
			map[string]any{
				"agent_id":  request.AgentID,
				"tool":      tc.Name,
				"iteration": request.Iteration,
			})

		// Create async callback for tools that implement AsyncTool
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
			channel,
			chatID,
			asyncCallback,
		)

		// NEW: Let the system decide whether to show tool results based on agent configuration
		shouldSendToUser := te.shouldSendToolResultToUser(agent, tc.Name, toolResult)

		// Send ForUser content to user based on system configuration, not tool-level silencing
		if shouldSendToUser && toolResult.ForUser != "" && sendResponse {
			// Publish as normal message
			te.msgBus.PublishOutbound(bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Content: toolResult.ForUser,
			})
			logger.DebugCF("agent", "Sent tool result to user",
				map[string]any{
					"tool":        tc.Name,
					"content_len": len(toolResult.ForUser),
				})
		}

		// Accumulate for-user content if needed
		if shouldSendToUser && toolResult.ForUser != "" {
			if forUserContent != "" {
				forUserContent += "\n\n" + toolResult.ForUser
			} else {
				forUserContent = toolResult.ForUser
			}
		}

		// Determine content for LLM based on tool result (always provide to LLM for context)
		contentForLLM := toolResult.ForLLM
		if contentForLLM == "" && toolResult.Err != nil {
			contentForLLM = toolResult.Err.Error()
		}

		// Add tool result to messages
		toolResultMsg := providers.Message{
			Role:       "tool",
			Content:    contentForLLM,
			ToolCallID: tc.ID,
		}
		results = append(results, toolResultMsg)
	}

	return &interfaces.ToolResult{
		AgentID:         request.AgentID,
		SessionKey:      request.SessionKey,
		Results:         results,
		ForUserContent:  forUserContent,
		CorrelationID:   request.CorrelationID,
	}, nil
}

// shouldSendToolResultToUser determines if a tool result should be sent to the user
// based on agent configuration and tool result characteristics, not tool-level silencing
func (te *ToolExecutor) shouldSendToolResultToUser(agent *interfaces.AgentInstance, toolName string, result *tools.ToolResult) bool {
	// If explicitly silenced by tool, respect that (backward compatibility)
	if result.Silent {
		return false
	}

	// If there's no content for user, don't send anything
	if result.ForUser == "" {
		return false
	}

	// NEW LOGIC: Use agent configuration to determine tool result visibility policy
	// This is where the system-level control comes in, instead of tool-level decisions

	// Default policy: respect SendToolHints configuration from agent
	// Since we don't have access to agent config here through interfaces.AgentInstance,
	// we'll use a simpler policy that relies on the result being non-empty
	// In a full implementation, this would check agent configuration like:
	// - agent.SendToolHints (boolean)
	// - agent.ToolVisibilityPolicy (enum: all, errors_only, whitelisted, none)
	// - agent.VisibleTools (whitelist of tool names)

	// For now, we'll return true to allow the system-level decision in the main agent loop
	// to handle this, and we'll rely on the tool result's ForUser field being populated
	// only when the tool result should be visible to the user

	return true // Allow sending if there's content for user
}

// Helper function to get min of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}