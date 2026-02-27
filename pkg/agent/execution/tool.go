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

		// Send ForUser content to user immediately if not Silent
		if !toolResult.Silent && toolResult.ForUser != "" && sendResponse {
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
		if !toolResult.Silent && toolResult.ForUser != "" {
			if forUserContent != "" {
				forUserContent += "\n\n" + toolResult.ForUser
			} else {
				forUserContent = toolResult.ForUser
			}
		}

		// Determine content for LLM based on tool result
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

// Helper function to get min of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}