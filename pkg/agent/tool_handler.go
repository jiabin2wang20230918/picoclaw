package agent

import (
	"context"
	"fmt"
	"sync"

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

// ProcessToolCalls executes tool calls concurrently and returns tool result messages
// in the same order as the input tool calls. Each tool runs in its own goroutine;
// results are collected into pre-allocated slots so ordering is preserved.
func (th *ToolHandler) ProcessToolCalls(
	ctx context.Context,
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

	// Pre-allocate result slots indexed by tool call position so concurrent
	// goroutines can write without contention and ordering is preserved.
	toolResultMessages := make([]providers.Message, len(normalizedToolCalls))

	// Mutex protects progress hint delivery (progressCallback is not goroutine-safe).
	var progressMu sync.Mutex

	var wg sync.WaitGroup
	for i, tc := range normalizedToolCalls {
		wg.Add(1)
		go func(idx int, call providers.ToolCall) {
			defer wg.Done()

			// Send progress notification (serialised via mutex).
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

			// Validate args if the tool implements ValidatingTool.
			// On failure write an error tool_result so the model can self-correct.
			if vt, ok := th.tools.GetAsValidating(call.Name); ok {
				if verr := vt.ValidateArgs(call.Arguments); verr != nil {
					logger.WarnCF("agent", "Tool argument validation failed",
						map[string]any{"tool": call.Name, "error": verr.Error()})
					toolResultMessages[idx] = providers.Message{
						Role:       "tool",
						Content:    fmt.Sprintf("Tool argument validation failed: %s", verr.Error()),
						ToolCallID: call.ID,
					}
					return
				}
			}

			// Async-completion callback (tool name captured via call parameter, not closure over loop var).
			asyncCallback := func(callbackCtx context.Context, result *tools.ToolResult) {
				if !result.Silent && result.ForUser != "" {
					logger.InfoCF("agent", "Async tool completed, agent will handle notification",
						map[string]any{
							"tool":        call.Name,
							"content_len": len(result.ForUser),
						})
				}
			}

			toolResult := th.tools.ExecuteWithContext(ctx, call.Name, call.Arguments, channel, chatID, asyncCallback)

			contentForLLM := toolResult.ForLLM
			if contentForLLM == "" && toolResult.Err != nil {
				contentForLLM = toolResult.Err.Error()
			}

			toolResultMessages[idx] = providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: call.ID,
			}
		}(i, tc)
	}

	wg.Wait()
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

