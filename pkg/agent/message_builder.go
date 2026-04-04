package agent

import (
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// CompactionConfig holds configuration for session compaction
type CompactionConfig struct {
	ReserveTokens        int
	KeepRecentTokens     int
	ReserveTokensFloor   int
	MemoryFlushEnabled   bool
	SoftThresholdTokens  int
}

// MessageBuilder is the primary runtime entrypoint for provider message assembly.
// It delegates canonical base-context construction to ContextBuilder, then applies
// token-budget and compaction policies on top.
type MessageBuilder struct {
	contextBuilder *ContextBuilder
}

func NewMessageBuilder(contextBuilder *ContextBuilder) *MessageBuilder {
	return &MessageBuilder{
		contextBuilder: contextBuilder,
	}
}

// BuildMessages constructs the complete message list for LLM interaction
func (mb *MessageBuilder) BuildMessages(
	history []providers.Message,
	summary, userMessage, memoryContext, media string,
	channel, chatID string,
) []providers.Message {
	messages := mb.composeMessages(history, summary, userMessage, memoryContext, channel, chatID)
	return mb.PruneToFitTokenLimit(messages)
}

// BuildMessagesWithConfig constructs the complete message list for LLM interaction with agent config
func (mb *MessageBuilder) BuildMessagesWithConfig(
	history []providers.Message,
	summary, userMessage, memoryContext, media string,
	channel, chatID string,
	compactionConfig CompactionConfig,
) []providers.Message {
	messages := mb.composeMessages(history, summary, userMessage, memoryContext, channel, chatID)
	return mb.PruneToFitTokenLimitWithConfig(messages, compactionConfig)
}

func (mb *MessageBuilder) composeMessages(
	history []providers.Message,
	summary, userMessage, memoryContext, channel, chatID string,
) []providers.Message {
	if mb.contextBuilder == nil {
		return nil
	}

	return mb.contextBuilder.AssembleBaseMessages(
		history,
		summary,
		userMessage,
		memoryContext,
		nil,
		channel,
		chatID,
	)
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

// PruneToFitTokenLimit trims messages to fit within token limits
func (mb *MessageBuilder) PruneToFitTokenLimit(messages []providers.Message) []providers.Message {
	// Use conservative token limit for Kimi API to avoid "Range of input length should be [1, 260096]" error
	// Use 200000 as a safe upper bound (about 77% of max to provide buffer for Kimi API stability)
	const maxSafeTokens = 200000

	tokenCount := mb.EstimateTokenCount(messages)
	if tokenCount <= maxSafeTokens {
		return messages // No pruning needed
	}

	// First, try to remove memory context if present
	var filteredMessages []providers.Message
	var memoryRemoved = false

	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "Relevant Past Memories") {
			memoryRemoved = true
			continue // Skip this memory context message
		}
		filteredMessages = append(filteredMessages, msg)
	}

	// Check if removing memory context helped
	if memoryRemoved {
		tokenCount = mb.EstimateTokenCount(filteredMessages)
		if tokenCount <= maxSafeTokens {
			return filteredMessages
		}
	}

	// If still over the limit, try removing all system messages except the first one
	var reducedMessages []providers.Message
	systemCount := 0
	for _, msg := range filteredMessages {
		if msg.Role == "system" {
			if systemCount == 0 {
				// Keep the first system message (usually contains important instructions)
				reducedMessages = append(reducedMessages, msg)
				systemCount++
			}
			// Skip other system messages (summaries, memory context, etc.)
		} else {
			// Keep non-system messages
			reducedMessages = append(reducedMessages, msg)
		}
	}

	tokenCount = mb.EstimateTokenCount(reducedMessages)
	if tokenCount <= maxSafeTokens {
		return reducedMessages
	}

	// If still over the limit, we need to use a more aggressive approach
	// Use more conservative compaction config for Kimi API
	compactionConfig := CompactionConfig{
		ReserveTokens:       8192,
		KeepRecentTokens:    12000,
		ReserveTokensFloor:  10000,
		MemoryFlushEnabled:  true,
		SoftThresholdTokens: 2000,
	}

	prunedMessages := mb.PruneSessionMemory("temp_session", reducedMessages, compactionConfig)

	// Final check - if still over limit, use simple truncation
	tokenCount = mb.EstimateTokenCount(prunedMessages)
	if tokenCount > maxSafeTokens {
		// Fallback: keep only the first system message + last 5 exchanges
		var finalMessages []providers.Message

		// Keep only the first system message
		for _, msg := range messages {
			if msg.Role == "system" {
				finalMessages = append(finalMessages, msg)
				break // Only add the first system message
			}
		}

		// Add the last exchanges
		lastExchanges := mb.GetLastExchanges(messages, 5) // Reduced from 10 to 5
		finalMessages = append(finalMessages, lastExchanges...)

		// If still too long, just take the most recent messages
		if mb.EstimateTokenCount(finalMessages) > maxSafeTokens {
			// Take just the last few messages with system message
			lastMessages := mb.getLastNMesssages(messages, 10) // Reduced from 15 to 10
			finalMessages = lastMessages
		}

		return finalMessages
	}

	return prunedMessages
}

// getLastNMesssages gets the last N messages from the list
func (mb *MessageBuilder) getLastNMesssages(messages []providers.Message, n int) []providers.Message {
	if len(messages) <= n {
		return messages
	}

	// Keep system messages but prioritize recent ones
	var result []providers.Message
	systemMsgs := []providers.Message{}
	otherMsgs := []providers.Message{}

	for _, msg := range messages {
		if msg.Role == "system" {
			systemMsgs = append(systemMsgs, msg)
		} else {
			otherMsgs = append(otherMsgs, msg)
		}
	}

	// Keep all system messages (or at least the first one)
	if len(systemMsgs) > 0 {
		result = append(result, systemMsgs[0]) // At least the main system message
		if len(systemMsgs) > 1 { // Include summary if available
			for _, sysMsg := range systemMsgs[1:] {
				if strings.Contains(sysMsg.Content, "Conversation Summary") {
					result = append(result, sysMsg)
					break
				}
			}
		}
	}

	// Take the last N-other messages
	startIdx := len(otherMsgs) - n
	if startIdx < 0 {
		startIdx = 0
	}

	result = append(result, otherMsgs[startIdx:]...)
	return result
}

// PruneToFitTokenLimitWithConfig trims messages to fit within token limits using provided configuration
func (mb *MessageBuilder) PruneToFitTokenLimitWithConfig(messages []providers.Message, compactionConfig CompactionConfig) []providers.Message {
	// Use conservative token limit for Kimi API to avoid "Range of input length should be [1, 260096]" error
	// Use 200000 as a safe upper bound (about 77% of max to provide buffer for Kimi API stability)
	const maxSafeTokens = 200000

	tokenCount := mb.EstimateTokenCount(messages)
	if tokenCount <= maxSafeTokens {
		return messages // No pruning needed
	}

	// First, try to remove memory context if present
	var filteredMessages []providers.Message
	var memoryRemoved = false

	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "Relevant Past Memories") {
			memoryRemoved = true
			continue // Skip this memory context message
		}
		filteredMessages = append(filteredMessages, msg)
	}

	// Check if removing memory context helped
	if memoryRemoved {
		tokenCount = mb.EstimateTokenCount(filteredMessages)
		if tokenCount <= maxSafeTokens {
			return filteredMessages
		}
	}

	// If still over the limit, try removing all system messages except the first one
	var reducedMessages []providers.Message
	systemCount := 0
	for _, msg := range filteredMessages {
		if msg.Role == "system" {
			if systemCount == 0 {
				// Keep the first system message (usually contains important instructions)
				reducedMessages = append(reducedMessages, msg)
				systemCount++
			}
			// Skip other system messages (summaries, memory context, etc.)
		} else {
			// Keep non-system messages
			reducedMessages = append(reducedMessages, msg)
		}
	}

	tokenCount = mb.EstimateTokenCount(reducedMessages)
	if tokenCount <= maxSafeTokens {
		return reducedMessages
	}

	// If still over the limit, use the smart pruning algorithm with the actual configuration
	prunedMessages := mb.PruneSessionMemory("temp_session", reducedMessages, compactionConfig)

	// Final check - if still over limit, use simple truncation
	tokenCount = mb.EstimateTokenCount(prunedMessages)
	if tokenCount > maxSafeTokens {
		// Fallback: keep only the first system message + last 5 exchanges (more aggressive)
		var finalMessages []providers.Message

		// Keep only the first system message
		for _, msg := range messages {
			if msg.Role == "system" {
				finalMessages = append(finalMessages, msg)
				break // Only add the first system message
			}
		}

		// Add the last exchanges
		lastExchanges := mb.GetLastExchanges(messages, 5) // Reduced from 10 to 5
		finalMessages = append(finalMessages, lastExchanges...)

		// If still too long, just take the most recent messages
		if mb.EstimateTokenCount(finalMessages) > maxSafeTokens {
			// Take just the last few messages with system message
			lastMessages := mb.getLastNMesssages(messages, 10) // Reduced from 15 to 10
			finalMessages = lastMessages
		}

		return finalMessages
	}

	return prunedMessages
}
