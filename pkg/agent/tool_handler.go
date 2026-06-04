package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/sipeed/quantclaw/pkg/bus"
	"github.com/sipeed/quantclaw/pkg/config"
	"github.com/sipeed/quantclaw/pkg/logger"
	"github.com/sipeed/quantclaw/pkg/providers"
	"github.com/sipeed/quantclaw/pkg/tools"
)

// ToolHandler handles all tool-related operations
type ToolHandler struct {
	config     *config.Config
	messageBus *bus.MessageBus
}

// NewToolHandler creates a new ToolHandler
func NewToolHandler(cfg *config.Config, messageBus *bus.MessageBus) *ToolHandler {
	return &ToolHandler{
		config:     cfg,
		messageBus: messageBus,
	}
}

// ProcessToolCalls executes tool calls according to documented execution policy.
// Serial tools run deterministically in order. Consecutive parallel-safe tools
// are executed concurrently as a batch while preserving output ordering.
func (th *ToolHandler) ProcessToolCalls(
	ctx context.Context,
	registry *tools.ToolRegistry,
	toolCalls []providers.ToolCall,
	channel, chatID string,
	progressCallback ProgressCallback,
	sessionKey string,
) ([]providers.Message, error) {
	// Normalize tool calls first (cheap, no concurrency needed)
	normalizedToolCalls := make([]providers.ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		normalizedToolCalls[i] = providers.NormalizeToolCall(tc)
	}

	toolResultMessages := make([]providers.Message, len(normalizedToolCalls))
	var progressMu sync.Mutex

	for i := 0; i < len(normalizedToolCalls); {
		tc := normalizedToolCalls[i]
		if th.executionPolicy(registry, tc.Name) == tools.ToolExecutionSerial {
			toolResultMessages[i] = th.executeToolCall(ctx, registry, tc, channel, chatID, progressCallback, &progressMu)
			i++
			continue
		}

		batchEnd := i
		for batchEnd < len(normalizedToolCalls) &&
			th.executionPolicy(registry, normalizedToolCalls[batchEnd].Name) == tools.ToolExecutionParallelSafe {
			batchEnd++
		}

		var wg sync.WaitGroup
		for idx := i; idx < batchEnd; idx++ {
			wg.Add(1)
			go func(resultIdx int, call providers.ToolCall) {
				defer wg.Done()
				toolResultMessages[resultIdx] = th.executeToolCall(
					ctx,
					registry,
					call,
					channel,
					chatID,
					progressCallback,
					&progressMu,
				)
			}(idx, normalizedToolCalls[idx])
		}
		wg.Wait()
		i = batchEnd
	}

	return toolResultMessages, nil
}

func (th *ToolHandler) executeToolCall(
	ctx context.Context,
	registry *tools.ToolRegistry,
	call providers.ToolCall,
	channel, chatID string,
	progressCallback ProgressCallback,
	progressMu *sync.Mutex,
) providers.Message {
	if th.config.Agents.Defaults.SendToolHints && progressCallback != nil {
		progressMu.Lock()
		err := progressCallback(0, fmt.Sprintf("Executing tool: %s", call.Name), map[string]interface{}{
			"tool_name":    call.Name,
			"phase":        "tool_execution",
			"is_completed": false,
		})
		progressMu.Unlock()
		if err != nil {
			logger.WarnCF("agent", "Failed to send tool hint", map[string]any{"error": err})
		}
	}

	if vt, ok := registry.GetAsValidating(call.Name); ok {
		if verr := vt.ValidateArgs(call.Arguments); verr != nil {
			logger.WarnCF("agent", "Tool argument validation failed",
				map[string]any{"tool": call.Name, "error": verr.Error()})
			return providers.Message{
				Role:       "tool",
				Content:    fmt.Sprintf("Tool argument validation failed: %s", verr.Error()),
				ToolCallID: call.ID,
			}
		}
	}

	asyncCallback := func(callbackCtx context.Context, result *tools.ToolResult) {
		if !result.Silent && result.ForUser != "" {
			logger.InfoCF("agent", "Async tool completed, agent will handle notification",
				map[string]any{
					"tool":        call.Name,
					"content_len": len(result.ForUser),
				})
		}
	}

	toolResult := registry.ExecuteWithContext(ctx, call.Name, call.Arguments, channel, chatID, asyncCallback)

	contentForLLM := toolResult.ForLLM
	if contentForLLM == "" && toolResult.Err != nil {
		contentForLLM = toolResult.Err.Error()
	}

	return providers.Message{
		Role:       "tool",
		Content:    contentForLLM,
		ToolCallID: call.ID,
	}
}

func (th *ToolHandler) executionPolicy(registry *tools.ToolRegistry, toolName string) tools.ToolExecutionPolicy {
	tool, ok := registry.Get(toolName)
	if !ok {
		return tools.ToolExecutionSerial
	}

	if aware, ok := tool.(tools.ExecutionPolicyAware); ok {
		return aware.ExecutionPolicy()
	}

	return tools.DefaultExecutionPolicy(toolName)
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
