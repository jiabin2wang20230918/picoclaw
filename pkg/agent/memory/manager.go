// Package memory handles long-term and daily memory persistence.
package memory

import (
	agent "github.com/sipeed/picoclaw/pkg/agent"
)

// Manager handles long-term and daily memory persistence.
type Manager struct {
	memoryStore *agent.MemoryStore
}

// NewManager creates a new memory manager instance.
func NewManager(workspace string) *Manager {
	return &Manager{
		memoryStore: agent.NewMemoryStore(workspace),
	}
}

// ReadLongTerm reads the long-term memory (MEMORY.md).
func (mm *Manager) ReadLongTerm() string {
	return mm.memoryStore.ReadLongTerm()
}

// WriteLongTerm writes content to the long-term memory file (MEMORY.md).
func (mm *Manager) WriteLongTerm(content string) error {
	return mm.memoryStore.WriteLongTerm(content)
}

// ReadToday reads today's daily note.
func (mm *Manager) ReadToday() string {
	return mm.memoryStore.ReadToday()
}

// AppendToday appends content to today's daily note.
func (mm *Manager) AppendToday(content string) error {
	return mm.memoryStore.AppendToday(content)
}

// GetRecentDailyNotes returns daily notes from the last N days.
func (mm *Manager) GetRecentDailyNotes(days int) string {
	return mm.memoryStore.GetRecentDailyNotes(days)
}

// GetMemoryContext returns formatted memory context for the agent prompt.
func (mm *Manager) GetMemoryContext() string {
	return mm.memoryStore.GetMemoryContext()
}