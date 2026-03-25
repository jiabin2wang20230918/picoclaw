package agent

import (
	"context"
	"fmt"
)

// WeclawAgentInterface defines the interface for a weclaw-compatible agent
type WeclawAgentInterface interface {
	Chat(ctx context.Context, conversationID string, message string) (string, error)
	ResetSession(ctx context.Context, conversationID string) (string, error)
	Info() AgentInfo
	SetCwd(cwd string)
}

// AgentInfo holds metadata about an agent for logging/debugging
type AgentInfo struct {
	Name    string // e.g. "claude-acp", "claude", "gpt-4o"
	Type    string // e.g. "acp", "cli", "http"
	Model   string // e.g. "sonnet", "gpt-4o-mini"
	Command string // binary path, e.g. "/usr/local/bin/claude-agent-acp"
	PID     int    // subprocess PID (0 if not applicable, e.g. http agent)
}

// PicoClawWeclawAdapter adapts PicoClaw's agent system to work with weclaw's agent interface
type PicoClawWeclawAdapter struct {
	loop  *AgentLoop
	model string
}

// NewPicoClawWeclawAdapter creates a new adapter that allows PicoClaw to work as a weclaw agent
func NewPicoClawWeclawAdapter(loop *AgentLoop, model string) *PicoClawWeclawAdapter {
	return &PicoClawWeclawAdapter{
		loop:  loop,
		model: model,
	}
}

// Chat sends a message to the PicoClaw agent and returns the response
func (a *PicoClawWeclawAdapter) Chat(ctx context.Context, conversationID string, message string) (string, error) {
	// Process the message directly with the default agent
	defaultAgent := a.loop.registry.GetDefaultAgent()
	if defaultAgent == nil {
		return "", fmt.Errorf("no default agent available")
	}

	// Use a fixed session key for the conversation, derived from the conversationID
	sessionKey := "agent:" + defaultAgent.ID + ":weclaw:" + conversationID

	// Process the message directly
	response, err := a.loop.ProcessDirectWithChannel(ctx, message, sessionKey, "weclaw", conversationID)
	if err != nil {
		return "", fmt.Errorf("failed to process message with PicoClaw: %w", err)
	}

	return response, nil
}

// ResetSession clears the existing session for the given conversationID
func (a *PicoClawWeclawAdapter) ResetSession(ctx context.Context, conversationID string) (string, error) {
	// In PicoClaw, we handle session reset by creating a new session key
	// For now, we'll just return an empty session ID, as PicoClaw handles sessions differently
	// Future enhancement could involve properly clearing session history
	return "", nil
}

// Info returns metadata about this agent
func (a *PicoClawWeclawAdapter) Info() AgentInfo {
	return AgentInfo{
		Name:    "picoclaw",
		Type:    "http",
		Model:   a.model,
		Command: "picoclaw",
		PID:     0, // Running as part of PicoClaw process
	}
}

// SetCwd changes the working directory for subsequent operations
func (a *PicoClawWeclawAdapter) SetCwd(cwd string) {
	// PicoClaw handles workspace at the agent configuration level
	// This is a no-op for now, but could be extended in the future to
	// influence file operation tools
}