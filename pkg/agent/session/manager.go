// Package session handles conversation history and session state management.
package session

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/agent/events"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

// Manager handles conversation history and session state.
type Manager struct {
	sessionManager *session.SessionManager
}

// NewManager creates a new session manager instance.
func NewManager(storageDir string) *Manager {
	return &Manager{
		sessionManager: session.NewSessionManager(storageDir),
	}
}

// UpdateSession updates the session state based on the event.
func (sm *Manager) UpdateSession(ctx context.Context, event *events.SessionUpdatedEvent) error {
	// Update the session with the new data
	sm.sessionManager.SetHistory(event.SessionKey, event.History)
	if event.Summary != "" {
		sm.sessionManager.SetSummary(event.SessionKey, event.Summary)
	}
	return sm.sessionManager.Save(event.SessionKey)
}

// GetHistory returns the conversation history for a session.
func (sm *Manager) GetHistory(sessionKey string) []providers.Message {
	return sm.sessionManager.GetHistory(sessionKey)
}

// GetSummary returns the summary for a session.
func (sm *Manager) GetSummary(sessionKey string) string {
	return sm.sessionManager.GetSummary(sessionKey)
}

// AddMessage adds a message to the session.
func (sm *Manager) AddMessage(sessionKey, role, content string) {
	sm.sessionManager.AddMessage(sessionKey, role, content)
}

// AddFullMessage adds a complete message with tool calls to the session.
func (sm *Manager) AddFullMessage(sessionKey string, msg providers.Message) {
	sm.sessionManager.AddFullMessage(sessionKey, msg)
}

// SetSummary sets the summary for a session.
func (sm *Manager) SetSummary(sessionKey string, summary string) {
	sm.sessionManager.SetSummary(sessionKey, summary)
}

// TruncateHistory truncates the history for a session, keeping the last N messages.
func (sm *Manager) TruncateHistory(sessionKey string, keepLast int) {
	sm.sessionManager.TruncateHistory(sessionKey, keepLast)
}

// Save saves the session to storage.
func (sm *Manager) Save(sessionKey string) error {
	return sm.sessionManager.Save(sessionKey)
}

// SetHistory replaces the entire history for a session.
func (sm *Manager) SetHistory(sessionKey string, history []providers.Message) {
	sm.sessionManager.SetHistory(sessionKey, history)
}