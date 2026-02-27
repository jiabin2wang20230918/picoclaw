// Package context handles building of message contexts for LLM processing.
package context

import (
	"strings"

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
	// This would integrate with the existing ContextBuilder from context.go
	// For now, we'll create a placeholder implementation

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