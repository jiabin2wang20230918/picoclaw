package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/quantclaw/pkg/bus"
	"github.com/sipeed/quantclaw/pkg/constants"
	"github.com/sipeed/quantclaw/pkg/logger"
	"github.com/sipeed/quantclaw/pkg/providers"
)

type InferenceRequest struct {
	Messages         []providers.Message
	ProviderToolDefs []providers.ToolDefinition
	Options          processOptions
}

type InferenceResult struct {
	Response     *providers.LLMResponse
	MessagesUsed []providers.Message
}

// InferenceService owns provider invocation policy: fallback chaining,
// context-overflow retries, and truncated-output continuation.
type InferenceService struct {
	bus           *bus.MessageBus
	fallback      *providers.FallbackChain
	contextBudget *ContextBudgetManager
}

func NewInferenceService(
	msgBus *bus.MessageBus,
	fallback *providers.FallbackChain,
	contextBudget *ContextBudgetManager,
) *InferenceService {
	return &InferenceService{
		bus:           msgBus,
		fallback:      fallback,
		contextBudget: contextBudget,
	}
}

func (is *InferenceService) Invoke(
	ctx context.Context,
	agent *AgentInstance,
	req InferenceRequest,
) (*InferenceResult, error) {
	messages := req.Messages

	logger.InfoCF("agent", "Calling LLM with tools",
		map[string]any{
			"tool_def_count": len(req.ProviderToolDefs),
			"message_count":  len(messages),
			"session_key":    req.Options.SessionKey,
			"model":          agent.Model,
		})

	maxRetries := 3
	for retry := 0; retry <= maxRetries; retry++ {
		response, err := is.callProvider(ctx, agent, messages, req.ProviderToolDefs)
		if err == nil {
			logger.InfoCF("agent", "LLM call successful",
				map[string]any{
					"has_content":     response.Content != "",
					"tool_call_count": len(response.ToolCalls),
					"session_key":     req.Options.SessionKey,
				})
			return &InferenceResult{
				Response:     response,
				MessagesUsed: messages,
			}, nil
		}

		if !isContextOverflowError(err.Error()) && !strings.Contains(strings.ToLower(err.Error()), "input length should be") {
			return nil, err
		}

		logger.WarnCF("agent", "Context window error detected, attempting compression",
			map[string]any{
				"error": err.Error(),
				"retry": retry,
			})

		if retry == 0 && !constants.IsInternalChannel(req.Options.Channel) {
			is.bus.PublishOutbound(bus.OutboundMessage{
				Channel: req.Options.Channel,
				ChatID:  req.Options.ChatID,
				Content: "Context window exceeded. Compressing history and retrying...",
			})
		}

		agent.SessionManager.ForceCompression(req.Options.SessionKey)
		messages = is.contextBudget.RebuildMessagesAfterCompression(
			agent,
			req.Options.SessionKey,
			req.Options.UserMessage,
			req.Options.MemoryContext,
			req.Options.Channel,
			req.Options.ChatID,
		)
	}

	return nil, fmt.Errorf("context overflow retry budget exhausted for session %s", req.Options.SessionKey)
}

func (is *InferenceService) ContinueAfterTruncation(
	ctx context.Context,
	agent *AgentInstance,
	messages []providers.Message,
	initial *providers.LLMResponse,
	opts processOptions,
) string {
	const maxContinuations = 3
	accumulated := initial.Content
	currentMessages := messages

	for i := 0; i < maxContinuations; i++ {
		currentMessages = append(currentMessages,
			providers.Message{Role: "assistant", Content: accumulated},
			providers.Message{Role: "user", Content: "Please continue from where you left off."},
		)
		currentMessages = is.contextBudget.PrepareContinuationMessages(agent, currentMessages)

		result, err := is.Invoke(ctx, agent, InferenceRequest{
			Messages:         currentMessages,
			ProviderToolDefs: agent.Tools.ToProviderDefs(),
			Options:          opts,
		})
		if err != nil {
			logger.WarnCF("agent", "Continuation call failed, returning accumulated content",
				map[string]any{
					"error":        err,
					"continuation": i + 1,
					"session_key":  opts.SessionKey,
				})
			break
		}

		accumulated += result.Response.Content
		currentMessages = result.MessagesUsed

		logger.InfoCF("agent", "Continuation succeeded",
			map[string]any{
				"continuation":  i + 1,
				"added_len":     len(result.Response.Content),
				"total_len":     len(accumulated),
				"finish_reason": result.Response.FinishReason,
				"session_key":   opts.SessionKey,
			})

		if result.Response.FinishReason != "max_tokens" && result.Response.FinishReason != "length" {
			break
		}
	}

	return accumulated
}

func (is *InferenceService) callProvider(
	ctx context.Context,
	agent *AgentInstance,
	messages []providers.Message,
	providerToolDefs []providers.ToolDefinition,
) (*providers.LLMResponse, error) {
	if len(agent.Candidates) > 1 && is.fallback != nil {
		fbResult, fbErr := is.fallback.Execute(
			ctx,
			agent.Candidates,
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
