// Package context handles building of message contexts for LLM processing.
package context

import (
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/agent/interfaces"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// ContextBuilder handles assembling system prompts and conversation context.
type ContextBuilder struct {
	workspace string
}

// NewContextBuilder creates a new context builder instance.
func NewContextBuilder(workspace string) *ContextBuilder {
	return &ContextBuilder{
		workspace: workspace,
	}
}

// BuildContext assembles the complete message context for LLM processing.
func (cb *ContextBuilder) BuildContext(agentID, sessionKey string, userMessage string, history []providers.Message, summary string) (*interfaces.ContextReady, error) {
	opts := interfaces.ProcessOptions{
		SessionKey:      sessionKey,
		Channel:         "", // This would come from context
		ChatID:          "", // This would come from context
		UserMessage:     userMessage,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
		NoHistory:       false,
	}

	return &interfaces.ContextReady{
		AgentID:    agentID,
		SessionKey: sessionKey,
		Messages:   buildMessages(history, summary, userMessage, opts),
		Options:    opts,
	}, nil
}

// buildMessages recreates the message building logic from the original loop.go
func buildMessages(history []providers.Message, summary string, currentMessage string, opts interfaces.ProcessOptions) []providers.Message {
	messages := []providers.Message{}

	// This would use the actual context builder from the original implementation
	// For now, we'll create a simplified version

	// Create a basic system message
	systemPrompt := "# picoclaw\n\nYou are picoclaw, a helpful AI assistant."
	if summary != "" {
		systemPrompt += "\n\n## Summary of Previous Conversation\n\n" + summary
	}

	history = sanitizeHistoryForProvider(history)

	messages = append(messages, providers.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	messages = append(messages, history...)

	if strings.TrimSpace(currentMessage) != "" {
		messages = append(messages, providers.Message{
			Role:    "user",
			Content: currentMessage,
		})
	}

	return messages
}

func sanitizeHistoryForProvider(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return history
	}

	sanitized := make([]providers.Message, 0, len(history))
	for _, msg := range history {
		switch msg.Role {
		case "tool":
			if len(sanitized) == 0 {
				continue
			}
			last := sanitized[len(sanitized)-1]
			if last.Role != "assistant" || len(last.ToolCalls) == 0 {
				continue
			}
			sanitized = append(sanitized, msg)

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				if len(sanitized) == 0 {
					continue
				}
				prev := sanitized[len(sanitized)-1]
				if prev.Role != "user" && prev.Role != "tool" {
					continue
				}
			}
			sanitized = append(sanitized, msg)

		default:
			sanitized = append(sanitized, msg)
		}
	}

	return sanitized
}

// AdaptiveContextBuilder enhances the basic context builder with intelligent compression and context management.
type AdaptiveContextBuilder struct {
	workspace string
	agentID   string
}

// NewAdaptiveContextBuilder creates a new adaptive context builder instance.
func NewAdaptiveContextBuilder(workspace, agentID string) *AdaptiveContextBuilder {
	return &AdaptiveContextBuilder{
		workspace: workspace,
		agentID:   agentID,
	}
}

// BuildAdaptiveContext builds context with intelligent compression and management.
func (ac *AdaptiveContextBuilder) BuildAdaptiveContext(agentID, sessionKey string, userMessage string, history []providers.Message, summary string, maxContext int) (*interfaces.ContextReady, error) {
	opts := interfaces.ProcessOptions{
		SessionKey:      sessionKey,
		Channel:         "", // This would come from context
		ChatID:          "", // This would come from context
		UserMessage:     userMessage,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
		NoHistory:       false,
	}

	// Apply intelligent context management
	messages := ac.buildAdaptiveMessages(history, summary, userMessage, maxContext)

	return &interfaces.ContextReady{
		AgentID:    agentID,
		SessionKey: sessionKey,
		Messages:   messages,
		Options:    opts,
	}, nil
}

// buildAdaptiveMessages implements intelligent context management with compression when needed.
func (ac *AdaptiveContextBuilder) buildAdaptiveMessages(history []providers.Message, summary string, currentMessage string, maxContext int) []providers.Message {
	// Calculate current context length
	currentLength := estimateContextLength(history, summary, currentMessage)

	if currentLength > maxContext {
		// Perform intelligent compression
		history = compressHistoryForContext(history, maxContext/2) // Keep half for history
	}

	// Build the messages with the possibly compressed history
	messages := []providers.Message{}

	// Create a basic system message
	systemPrompt := "# picoclaw\n\nYou are picoclaw, a helpful AI assistant."
	if summary != "" {
		systemPrompt += "\n\n## Summary of Previous Conversation\n\n" + summary
	}

	history = sanitizeHistoryForProvider(history)

	messages = append(messages, providers.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	messages = append(messages, history...)

	if strings.TrimSpace(currentMessage) != "" {
		messages = append(messages, providers.Message{
			Role:    "user",
			Content: currentMessage,
		})
	}

	return messages
}

// compressHistoryForContext intelligently compresses the conversation history when context limits are approached.
func compressHistoryForContext(history []providers.Message, targetMaxLength int) []providers.Message {
	if len(history) <= 4 { // Keep minimum history
		return history
	}

	// Estimate current length and compress if needed
	currentLength := estimateTotalMessagesLength(history)
	if currentLength < targetMaxLength {
		return history // No compression needed
	}

	// Keep system prompt and the last message (most important), compress middle
	// Keep the last 40% of messages and drop the oldest 60%
	keepCount := len(history) * 40 / 100
	if keepCount < 2 {
		keepCount = 2
	}

	// Always keep the first message (system) and last few messages (context)
	startIndex := len(history) - keepCount
	if startIndex < 1 { // Ensure we don't lose the first message
		startIndex = 1
	}

	trimmedHistory := make([]providers.Message, 0, keepCount+1)
	trimmedHistory = append(trimmedHistory, history[0])           // Keep first (usually system)
	trimmedHistory = append(trimmedHistory, history[startIndex:]...) // Keep last N messages

	return trimmedHistory
}

// estimateContextLength estimates the total length of the context.
func estimateContextLength(history []providers.Message, summary string, currentMessage string) int {
	total := 0
	for _, msg := range history {
		total += utf8.RuneCountInString(msg.Content)
	}
	total += utf8.RuneCountInString(summary)
	total += utf8.RuneCountInString(currentMessage)
	return total
}

// estimateTotalMessagesLength estimates the total length of all messages.
func estimateTotalMessagesLength(messages []providers.Message) int {
	total := 0
	for _, msg := range messages {
		total += utf8.RuneCountInString(msg.Content)
	}
	return total
}