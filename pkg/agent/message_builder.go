package agent

import (
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// MessageBuilder constructs messages for LLM with proper context
type MessageBuilder struct {
	sessionManager *SessionManager
}

func NewMessageBuilder(sessionManager *SessionManager) *MessageBuilder {
	return &MessageBuilder{
		sessionManager: sessionManager,
	}
}

// BuildMessages constructs the complete message list for LLM interaction
func (mb *MessageBuilder) BuildMessages(
	history []providers.Message,
	summary, userMessage, memoryContext, media string,
	channel, chatID string,
) []providers.Message {
	// This would use the ContextBuilder from the agent instance
	// For now, returning a basic implementation
	return append(history, providers.Message{
		Role:    "user",
		Content: userMessage,
	})
}

// EstimateTokenCount estimates the number of tokens in a message list
func (mb *MessageBuilder) EstimateTokenCount(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += utf8.RuneCountInString(m.Content)
	}
	// 2.5 chars per token = totalChars * 2 / 5
	return totalChars * 2 / 5
}

// PruneSessionMemory performs in-memory trimming of old tool results without
// rewriting the persistent history, similar to openclaw's session pruning mechanism
func (mb *MessageBuilder) PruneSessionMemory(sessionKey string, messages []providers.Message, compactionConfig CompactionConfig) []providers.Message {
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
		msgTokens := mb.EstimateTokenCount([]providers.Message{msg})

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
							toolMsgTokens := mb.EstimateTokenCount([]providers.Message{nextMsg})
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

	return prunedMessages
}

// GetLastExchanges returns the last N exchanges (user-assistant pairs) from the history
func (mb *MessageBuilder) GetLastExchanges(history []providers.Message, n int) []providers.Message {
	var exchanges []providers.Message
	userMsgFound := false

	// Process from the end backwards
	for i := len(history) - 1; i >= 0 && n > 0; i-- {
		msg := history[i]

		if msg.Role == "user" {
			userMsgFound = true
			exchanges = append([]providers.Message{msg}, exchanges...)
			n--
		} else if msg.Role == "assistant" && userMsgFound {
			exchanges = append([]providers.Message{msg}, exchanges...)
			userMsgFound = false
		} else if msg.Role == "tool" {
			// Include tool messages that are part of the exchange
			exchanges = append([]providers.Message{msg}, exchanges...)
		} else if msg.Role == "system" {
			// Include system message if it's the first one
			if len(exchanges) == 0 || exchanges[0].Role != "system" {
				exchanges = append([]providers.Message{msg}, exchanges...)
			}
		}
	}

	return exchanges
}

// CompactionConfig holds configuration for session compaction
type CompactionConfig struct {
	ReserveTokens        int
	KeepRecentTokens     int
	ReserveTokensFloor   int
	MemoryFlushEnabled   bool
	SoftThresholdTokens  int
}