package agent

import "github.com/sipeed/picoclaw/pkg/providers"

// ContextBudgetManager centralizes token-budget and context-compaction policy.
// It owns lightweight heuristics and message rebuilding decisions, while the
// loop remains responsible for orchestration and persistence.
type ContextBudgetManager struct{}

func NewContextBudgetManager() *ContextBudgetManager {
	return &ContextBudgetManager{}
}

func (cbm *ContextBudgetManager) BuildCompactionConfig(agent *AgentInstance) CompactionConfig {
	return CompactionConfig{
		ReserveTokens:       agent.CompactionConfig.ReserveTokens,
		KeepRecentTokens:    agent.CompactionConfig.KeepRecentTokens,
		ReserveTokensFloor:  agent.CompactionConfig.ReserveTokensFloor,
		MemoryFlushEnabled:  agent.CompactionConfig.MemoryFlush.Enabled,
		SoftThresholdTokens: agent.CompactionConfig.MemoryFlush.SoftThresholdTokens,
	}
}

func (cbm *ContextBudgetManager) BuildMessages(
	agent *AgentInstance,
	history []providers.Message,
	summary string,
	userMessage string,
	memoryContext string,
	channel string,
	chatID string,
) []providers.Message {
	return agent.MessageBuilder.BuildMessagesWithConfig(
		history,
		summary,
		userMessage,
		memoryContext,
		"",
		channel,
		chatID,
		cbm.BuildCompactionConfig(agent),
	)
}

func (cbm *ContextBudgetManager) RebuildMessagesAfterCompression(
	agent *AgentInstance,
	sessionKey string,
	userMessage string,
	memoryContext string,
	channel string,
	chatID string,
) []providers.Message {
	return cbm.BuildMessages(
		agent,
		agent.SessionManager.GetHistory(sessionKey),
		agent.SessionManager.GetSummary(sessionKey),
		userMessage,
		memoryContext,
		channel,
		chatID,
	)
}

// EstimateTokens uses the runtime message builder's heuristic so token
// estimation policy stays in one place.
func (cbm *ContextBudgetManager) EstimateTokens(agent *AgentInstance, messages []providers.Message) int {
	if agent != nil && agent.MessageBuilder != nil {
		return agent.MessageBuilder.EstimateTokenCount(messages)
	}

	totalChars := 0
	for _, m := range messages {
		totalChars += len([]rune(m.Content))
	}
	return totalChars * 2 / 5
}

func (cbm *ContextBudgetManager) PrepareContinuationMessages(agent *AgentInstance, messages []providers.Message) []providers.Message {
	if len(messages) == 0 {
		return messages
	}

	targetTokens := cbm.EstimateTokens(agent, messages) * 4 / 5
	if agent.CompactionConfig.ReserveTokens > 0 {
		targetTokens = agent.CompactionConfig.ReserveTokens * 10
	}

	for len(messages) > 2 && cbm.EstimateTokens(agent, messages) > targetTokens {
		dropped := false
		for i := 1; i < len(messages)-2; i++ {
			if messages[i].Role != "system" {
				messages = append(messages[:i], messages[i+1:]...)
				dropped = true
				break
			}
		}
		if !dropped {
			break
		}
	}

	return messages
}

func (cbm *ContextBudgetManager) ShouldTriggerMemoryFlush(agent *AgentInstance, messages []providers.Message) bool {
	if agent == nil || !agent.CompactionConfig.MemoryFlush.Enabled {
		return false
	}

	tokenEstimate := cbm.EstimateTokens(agent, messages)
	reserveTokensFloor := agent.CompactionConfig.ReserveTokensFloor
	softThresholdTokens := agent.CompactionConfig.MemoryFlush.SoftThresholdTokens
	memoryFlushThreshold := agent.ContextWindow - reserveTokensFloor - softThresholdTokens

	return tokenEstimate > memoryFlushThreshold
}
