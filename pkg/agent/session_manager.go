package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/quantclaw/pkg/agent/interfaces"
	"github.com/sipeed/quantclaw/pkg/providers"
	session "github.com/sipeed/quantclaw/pkg/session"
	"github.com/sipeed/quantclaw/pkg/utils"
)

// SessionManager wraps the low-level session.SessionManager and provides the
// higher-level interface used by the agent loop (and satisfies interfaces.SessionManager
// so it can be passed to orchestrator components when the modular path is active).
type SessionManager struct {
	sessions *session.SessionManager
}

// NewSessionManager creates a SessionManager backed by the given low-level manager.
// Passing agent.Sessions directly decouples the manager from any single AgentInstance,
// making multi-agent routing safe.
func NewSessionManager(sessions *session.SessionManager) *SessionManager {
	return &SessionManager{sessions: sessions}
}

// ---------------------------------------------------------------------------
// interfaces.SessionManager implementation
// ---------------------------------------------------------------------------

// UpdateSession applies a SessionUpdated event (history + optional summary).
func (sm *SessionManager) UpdateSession(_ context.Context, event *interfaces.SessionUpdated) error {
	sm.sessions.SetHistory(event.SessionKey, event.History)
	if event.Summary != "" {
		sm.sessions.SetSummary(event.SessionKey, event.Summary)
	}
	return sm.sessions.Save(event.SessionKey)
}

// GetHistory returns the message history for the session.
func (sm *SessionManager) GetHistory(sessionKey string) []providers.Message {
	return sm.sessions.GetHistory(sessionKey)
}

// GetSummary returns the conversation summary for the session.
func (sm *SessionManager) GetSummary(sessionKey string) string {
	return sm.sessions.GetSummary(sessionKey)
}

// AddMessage appends a simple role/content message to the session.
func (sm *SessionManager) AddMessage(sessionKey, role, content string) {
	sm.sessions.AddMessage(sessionKey, role, content)
}

// AddFullMessage appends a complete message (including tool call fields) to the session.
func (sm *SessionManager) AddFullMessage(sessionKey string, msg providers.Message) {
	sm.sessions.AddFullMessage(sessionKey, msg)
}

// SetSummary updates the conversation summary for the session.
func (sm *SessionManager) SetSummary(sessionKey string, summary string) {
	sm.sessions.SetSummary(sessionKey, summary)
}

// TruncateHistory keeps only the last keepLast messages.
func (sm *SessionManager) TruncateHistory(sessionKey string, keepLast int) {
	sm.sessions.TruncateHistory(sessionKey, keepLast)
}

// Save persists the session to disk.
func (sm *SessionManager) Save(sessionKey string) error {
	return sm.sessions.Save(sessionKey)
}

// SetHistory replaces the entire message history for the session.
func (sm *SessionManager) SetHistory(sessionKey string, history []providers.Message) {
	sm.sessions.SetHistory(sessionKey, history)
}

// ---------------------------------------------------------------------------
// Convenience helpers used by the agent loop
// ---------------------------------------------------------------------------

// AddUserMessage is a convenience wrapper around AddMessage for user turns.
func (sm *SessionManager) AddUserMessage(sessionKey, content string) {
	sm.sessions.AddMessage(sessionKey, "user", content)
}

// AddAssistantMessage is a convenience wrapper around AddMessage for assistant turns.
func (sm *SessionManager) AddAssistantMessage(sessionKey, content string) {
	sm.sessions.AddMessage(sessionKey, "assistant", content)
}

// AddToolResult appends a tool-result message (which carries ToolCallID) to the session.
func (sm *SessionManager) AddToolResult(sessionKey string, msg providers.Message) {
	sm.sessions.AddFullMessage(sessionKey, msg)
}

// ---------------------------------------------------------------------------
// Context compression
// ---------------------------------------------------------------------------

// ForceCompression aggressively compresses the session history when the context
// window is exceeded. It groups messages by tool-call relationships, collapses
// the oldest 75 % of groups into compact summary lines, and keeps the most
// recent 25 % of groups verbatim. This matches the strategy used by
// AgentLoop.forceCompression and supersedes the older half-keep approach.
func (sm *SessionManager) ForceCompression(sessionKey string) {
	history := sm.sessions.GetHistory(sessionKey)
	if len(history) <= 4 {
		return
	}

	grouped := smGroupRelatedMessages(history)

	// Keep the most recent 25 % of groups (at least 2).
	keepCount := len(grouped) / 4
	if keepCount < 2 {
		keepCount = 2
	}
	startIdx := len(grouped) - keepCount
	if startIdx < 0 {
		startIdx = 0
	}

	var newHistory []providers.Message

	// Preserve system message with a compression notice.
	if len(history) > 0 && history[0].Role == "system" {
		baseContent := strings.SplitN(history[0].Content, "\n\n[System Note:", 2)[0]
		notice := fmt.Sprintf(
			"\n\n[System Note: History compressed at %s due to length. Earlier context collapsed.]",
			time.Now().Format("2006-01-02 15:04:05"),
		)
		newHistory = append(newHistory, providers.Message{
			Role:    "system",
			Content: baseContent + notice,
		})
	}

	// Collapse dropped groups into compact one-line summaries.
	for i := 0; i < startIdx; i++ {
		if summary := smCollapseGroup(grouped[i]); summary != "" {
			newHistory = append(newHistory, providers.Message{
				Role:    "system",
				Content: "[Collapsed] " + summary,
			})
		}
	}

	// Append the most recent groups verbatim.
	for i := startIdx; i < len(grouped); i++ {
		newHistory = append(newHistory, grouped[i]...)
	}

	sm.sessions.SetHistory(sessionKey, newHistory)
	_ = sm.sessions.Save(sessionKey)
}

// smGroupRelatedMessages groups a message slice by tool-call relationships so
// that assistant+tool_result pairs are never split across groups.
func smGroupRelatedMessages(messages []providers.Message) [][]providers.Message {
	var groups [][]providers.Message
	var current []providers.Message

	for i, msg := range messages {
		current = append(current, msg)

		switch {
		case msg.Role == "tool":
			// Close the group when the last tool result for an assistant turn arrives.
			for j := len(current) - 1; j >= 0; j-- {
				if current[j].Role == "assistant" && len(current[j].ToolCalls) > 0 {
					for _, tc := range current[j].ToolCalls {
						if tc.ID == msg.ToolCallID {
							groups = append(groups, current)
							current = nil
							break
						}
					}
					break
				}
			}

		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			// Wait for the matching tool results before closing.

		default:
			// User / system messages close the group unless the next message is a tool result.
			if i+1 < len(messages) && messages[i+1].Role != "tool" {
				groups = append(groups, current)
				current = nil
			}
		}
	}

	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// smCollapseGroup condenses a message group into a single human-readable line.
func smCollapseGroup(group []providers.Message) string {
	var parts []string
	for _, msg := range group {
		switch msg.Role {
		case "user":
			parts = append(parts, "User: "+utils.Truncate(msg.Content, 80))
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				names := make([]string, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					names = append(names, tc.Name)
				}
				parts = append(parts, "Called: ["+strings.Join(names, ", ")+"]")
			} else if msg.Content != "" {
				parts = append(parts, "Assistant: "+utils.Truncate(msg.Content, 80))
			}
		case "tool":
			id := msg.ToolCallID
			if len(id) > 8 {
				id = id[:8]
			}
			parts = append(parts, fmt.Sprintf("Tool(%s): %s", id, utils.Truncate(msg.Content, 60)))
		}
	}
	return strings.Join(parts, " → ")
}
