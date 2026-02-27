// Package interfaces defines the interfaces for the PicoClaw agent modular components.
package interfaces

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// MessageRouter routes incoming messages to appropriate agents.
type MessageRouter interface {
	RouteMessage(msg bus.InboundMessage) (*RoutingDecision, error)
}

// RoutingDecision represents the decision of which agent should handle a message.
type RoutingDecision struct {
	OriginalMessage bus.InboundMessage
	AgentID         string
	SessionKey      string
	RouteType       string // direct, default, etc.
}

// ContextBuilder assembles system prompts and conversation context.
type ContextBuilder interface {
	BuildContext(agentID, sessionKey string, userMessage string, history []providers.Message, summary string) (*ContextReady, error)
}

// ContextReady represents a fully assembled message context ready for LLM processing.
type ContextReady struct {
	AgentID    string
	SessionKey string
	Messages   []providers.Message // Complete context for LLM
	Options    ProcessOptions
}

// ProcessOptions configures how a message is processed.
type ProcessOptions struct {
	SessionKey      string // Session identifier for history/context
	Channel         string // Target channel for tool execution
	ChatID          string // Target chat ID for tool execution
	UserMessage     string // User message content (may include prefix)
	DefaultResponse string // Response when LLM returns empty
	EnableSummary   bool   // Whether to trigger summarization
	SendResponse    bool   // Whether to send response via bus
	NoHistory       bool   // If true, don't load session history (for heartbeat)
}

// LLMOrchestrator manages LLM interaction cycles and tool loops.
type LLMOrchestrator interface {
	ProcessContext(ctx context.Context, agent *AgentInstance, event *ContextReady) (*LLMResponse, error)
}

// LLMResponse represents a response from the LLM after processing.
type LLMResponse struct {
	AgentID        string
	SessionKey     string
	Response       providers.LLMResponse
	FinalContent   string
	Iteration      int
}

// ToolExecutor executes tools and manages results.
type ToolExecutor interface {
	ExecuteTools(ctx context.Context, agent *AgentInstance, event *ToolRequest) (*ToolResult, error)
}

// ToolRequest represents a request to execute one or more tools.
type ToolRequest struct {
	AgentID       string
	SessionKey    string
	ToolCalls     []providers.ToolCall
	Iteration     int
	CorrelationID string
}

// ToolResult represents the result of executing tools.
type ToolResult struct {
	AgentID         string
	SessionKey      string
	Results         []providers.Message // Tool results formatted as messages
	ForUserContent  string              // Content to send directly to user
	CorrelationID   string
}

// SessionManager handles conversation history and session state.
type SessionManager interface {
	UpdateSession(ctx context.Context, event *SessionUpdated) error
	GetHistory(sessionKey string) []providers.Message
	GetSummary(sessionKey string) string
	AddMessage(sessionKey, role, content string)
	AddFullMessage(sessionKey string, msg providers.Message)
	SetSummary(sessionKey string, summary string)
	TruncateHistory(sessionKey string, keepLast int)
	Save(sessionKey string) error
	SetHistory(sessionKey string, history []providers.Message)
}

// SessionUpdated represents an update to session state.
type SessionUpdated struct {
	SessionKey string
	History    []providers.Message
	Summary    string
	AgentID    string
}

// MemoryManager handles long-term and daily memory persistence.
type MemoryManager interface {
	ReadLongTerm() string
	WriteLongTerm(content string) error
	ReadToday() string
	AppendToday(content string) error
	GetRecentDailyNotes(days int) string
	GetMemoryContext() string
}

// AgentSupervisor coordinates overall agent lifecycle and component orchestration.
type AgentSupervisor interface {
	Initialize(ctx context.Context, cfg *config.Config, msgBus *bus.MessageBus, provider providers.LLMProvider) error
	Run(ctx context.Context) error
	Stop() error
	RegisterTool(tool tools.Tool)
	ProcessDirect(ctx context.Context, content, sessionKey string) (string, error)
	ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error)
	ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error)
}

// AgentInstance represents a fully configured agent with its own workspace,
// session manager, context builder, and tool registry.
// This mirrors the existing AgentInstance struct for compatibility.
type AgentInstance struct {
	ID             string
	Name           string
	Model          string
	Fallbacks      []string
	Workspace      string
	MaxIterations  int
	MaxTokens      int
	Temperature    float64
	ContextWindow  int
	Provider       providers.LLMProvider
	Sessions       *session.SessionManager
	ContextBuilder *ContextBuilder
	Tools          *tools.ToolRegistry
	Subagents      *config.SubagentsConfig
	SkillsFilter   []string
	Candidates     []providers.FallbackCandidate
}