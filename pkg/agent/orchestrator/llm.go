// Package orchestrator handles LLM interactions and tool execution loops.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/agent/interfaces"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// LLMOrchestrator manages LLM interaction cycles and tool loops.
type LLMOrchestrator struct {
	msgBus       *bus.MessageBus
	fallback     *providers.FallbackChain
	sessionMgr   interfaces.SessionManager
	toolExecutor interfaces.ToolExecutor
}

// NewLLMOrchestrator creates a new LLM orchestrator instance.
func NewLLMOrchestrator(msgBus *bus.MessageBus, fallback *providers.FallbackChain, sessionMgr interfaces.SessionManager, toolExecutor interfaces.ToolExecutor) *LLMOrchestrator {
	return &LLMOrchestrator{
		msgBus:       msgBus,
		fallback:     fallback,
		sessionMgr:   sessionMgr,
		toolExecutor: toolExecutor,
	}
}

// ProcessContext executes the LLM processing cycle with tool handling.
func (lo *LLMOrchestrator) ProcessContext(ctx context.Context, agent *interfaces.AgentInstance, event *interfaces.ContextReady) (*interfaces.LLMResponse, error) {
	// Run LLM iteration loop
	finalContent, iteration, err := lo.runLLMIteration(ctx, agent, event.Messages, event.Options)
	if err != nil {
		return nil, err
	}

	// Handle empty response
	if finalContent == "" {
		finalContent = event.Options.DefaultResponse
	}

	// Add final assistant message to session
	lo.sessionMgr.AddMessage(event.SessionKey, "assistant", finalContent)
	lo.sessionMgr.Save(event.SessionKey)

	return &interfaces.LLMResponse{
		AgentID:      event.AgentID,
		SessionKey:   event.SessionKey,
		FinalContent: finalContent,
		Iteration:    iteration,
	}, nil
}

// runLLMIteration executes the LLM call loop with tool handling.
func (lo *LLMOrchestrator) runLLMIteration(
	ctx context.Context,
	agent *interfaces.AgentInstance,
	messages []providers.Message,
	opts interfaces.ProcessOptions,
) (string, int, error) {
	iteration := 0
	var finalContent string

	for iteration < agent.MaxIterations {
		iteration++

		logger.DebugCF("agent", "LLM iteration",
			map[string]any{
				"agent_id":  agent.ID,
				"iteration": iteration,
				"max":       agent.MaxIterations,
			})

		// Build tool definitions
		providerToolDefs := agent.Tools.ToProviderDefs()

		// Call LLM with fallback chain if candidates are configured.
		response, err := lo.callLLM(ctx, agent, messages, providerToolDefs)
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

		// Normalize tool calls
		normalizedToolCalls := make([]providers.ToolCall, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			normalizedToolCalls = append(normalizedToolCalls, providers.NormalizeToolCall(tc))
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
		lo.sessionMgr.AddFullMessage(opts.SessionKey, assistantMsg)

		// Execute tool calls using the tool executor component
		toolRequest := &interfaces.ToolRequest{
			AgentID:       agent.ID,
			SessionKey:    opts.SessionKey,
			ToolCalls:     normalizedToolCalls,
			Iteration:     iteration,
			CorrelationID: fmt.Sprintf("%s-%d", opts.SessionKey, iteration),
		}

		toolResult, err := lo.toolExecutor.ExecuteTools(ctx, agent, toolRequest)
		if err != nil {
			logger.ErrorCF("agent", "Tool execution failed",
				map[string]any{
					"agent_id": agent.ID,
					"error":    err.Error(),
				})
			return "", iteration, fmt.Errorf("tool execution failed: %w", err)
		}

		// Add tool results to messages
		for _, resultMsg := range toolResult.Results {
			messages = append(messages, resultMsg)
		}

		// Save tool result messages to session
		for _, resultMsg := range toolResult.Results {
			lo.sessionMgr.AddFullMessage(opts.SessionKey, resultMsg)
		}
	}

	return finalContent, iteration, nil
}

// callLLM handles calling the LLM with fallback mechanisms.
func (lo *LLMOrchestrator) callLLM(ctx context.Context, agent *interfaces.AgentInstance, messages []providers.Message, providerToolDefs []providers.ToolDefinition) (*providers.LLMResponse, error) {
	// Call LLM with fallback chain if candidates are configured.
	var response *providers.LLMResponse
	var err error

	if len(agent.Candidates) > 1 && lo.fallback != nil {
		fbResult, fbErr := lo.fallback.Execute(ctx, agent.Candidates,
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
				map[string]any{"agent_id": agent.ID, "iteration": 1})
		}
		response = fbResult.Response
		return response, nil
	}

	response, err = agent.Provider.Chat(ctx, messages, providerToolDefs, agent.Model, map[string]any{
		"max_tokens":  agent.MaxTokens,
		"temperature": agent.Temperature,
	})

	return response, err
}