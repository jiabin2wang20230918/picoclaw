package agent

import (
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// SessionManager handles all session-related operations
type SessionManager struct {
	agent *AgentInstance
}

func NewSessionManager(agent *AgentInstance) *SessionManager {
	return &SessionManager{
		agent: agent,
	}
}

// AddUserMessage adds a user message to the session
func (sm *SessionManager) AddUserMessage(sessionKey, content string) {
	sm.agent.Sessions.AddMessage(sessionKey, "user", content)
}

// AddAssistantMessage adds an assistant message to the session
func (sm *SessionManager) AddAssistantMessage(sessionKey, content string) {
	sm.agent.Sessions.AddMessage(sessionKey, "assistant", content)
}

// AddToolResult adds a tool result message to the session
func (sm *SessionManager) AddToolResult(sessionKey string, msg providers.Message) {
	sm.agent.Sessions.AddFullMessage(sessionKey, msg)
}

// GetHistory retrieves the session history
func (sm *SessionManager) GetHistory(sessionKey string) []providers.Message {
	return sm.agent.Sessions.GetHistory(sessionKey)
}

// GetSummary retrieves the session summary
func (sm *SessionManager) GetSummary(sessionKey string) string {
	return sm.agent.Sessions.GetSummary(sessionKey)
}

// Save saves the session state
func (sm *SessionManager) Save(sessionKey string) {
	sm.agent.Sessions.Save(sessionKey)
}

// ForceCompression compresses the session history when it exceeds limits
func (sm *SessionManager) ForceCompression(sessionKey string) {
	history := sm.GetHistory(sessionKey)
	if len(history) <= 4 {
		return
	}

	// Implementation moved from loop.go
	// Identify tool call-result pairs to preserve
	// Group messages by tool call relationships to maintain integrity
	groupedMessages := sm.groupRelatedMessages(history)

	// Calculate how many groups to keep (preserve tool call/result relationships)
	keepCount := len(groupedMessages) / 2 // Keep half of the message groups
	if keepCount < 2 {
		keepCount = 2 // Ensure we keep at least 2 groups
	}

	// Start with system message if present, then take the most recent groups
	var newHistory []providers.Message

	// Preserve system message at the beginning
	if len(history) > 0 && history[0].Role == "system" {
		// Extract any compression notes that might already be present
		var baseContent string
		contentParts := split(history[0].Content, "\n\n[System Note:")
		if len(contentParts) > 0 {
			baseContent = contentParts[0]
		}
		// Create updated system message with compression notice
		compressionNotice := "[System Note: History compressed at " + time.Now().Format("2006-01-02 15:04:05") + " due to length. Earlier context preserved in context but truncated for current exchange.]"
		newHistory = append(newHistory, providers.Message{
			Role:    "system",
			Content: baseContent + "\n\n" + compressionNotice,
		})
	}

	// Add remaining message groups
	startIdx := len(groupedMessages) - keepCount
	if startIdx < 0 {
		startIdx = 0
	}

	for i := startIdx; i < len(groupedMessages); i++ {
		newHistory = append(newHistory, groupedMessages[i]...)
	}

	// Update session with compressed history
	sm.agent.Sessions.SetHistory(sessionKey, newHistory)
	sm.Save(sessionKey)
}

// groupRelatedMessages groups messages by their relationships (especially tool call-result pairs)
func (sm *SessionManager) groupRelatedMessages(messages []providers.Message) [][]providers.Message {
	var groups [][]providers.Message
	var currentGroup []providers.Message

	for i, msg := range messages {
		currentGroup = append(currentGroup, msg)

		// If this is a tool message, close the group
		if msg.Role == "tool" {
			// Find the corresponding assistant message that initiated the tool call
			for j := len(currentGroup) - 1; j >= 0; j-- {
				if currentGroup[j].Role == "assistant" && len(currentGroup[j].ToolCalls) > 0 {
					// Check if any of the assistant's tool calls matches this tool's call ID
					for _, tc := range currentGroup[j].ToolCalls {
						if tc.ID == msg.ToolCallID {
							groups = append(groups, currentGroup)
							currentGroup = []providers.Message{}
							break
						}
					}
					break
				}
			}
		} else if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			// Wait for the corresponding tool results
			continue
		} else {
			// For user/system messages, close the group if it's not part of a tool call chain
			if i+1 < len(messages) && messages[i+1].Role != "tool" {
				groups = append(groups, currentGroup)
				currentGroup = []providers.Message{}
			}
		}
	}

	// Add remaining messages as a group
	if len(currentGroup) > 0 {
		groups = append(groups, currentGroup)
	}

	return groups
}

// split is a helper function similar to strings.Split
func split(s, sep string) []string {
	return strings.Split(s, sep)
}