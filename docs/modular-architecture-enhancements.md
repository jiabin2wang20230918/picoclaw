# PicoClaw Agent - Enhanced Modular Architecture

This document describes the enhanced modular architecture of the PicoClaw agent system, including the new adaptive context handling and permissions management features.

## Architecture Overview

The PicoClaw agent has been refactored from a monolithic structure into a modular architecture with clearly separated concerns:

- **Router**: Handles message routing to appropriate agents
- **Context Builder**: Builds context for LLM processing with adaptive compression
- **LLM Orchestrator**: Manages LLM interactions and tool loops
- **Tool Executor**: Executes tools with permission enforcement
- **Session Manager**: Manages conversation history and state
- **Memory Manager**: Handles long-term and daily memory persistence
- **Permissions Manager**: Enforces tool access controls per agent/channel
- **Supervisor**: Coordinates overall agent lifecycle

## New Features

### 1. Adaptive Context Handling

The new `AdaptiveContextBuilder` provides intelligent context management:

- Proactively monitors context length to prevent hitting token limits
- Automatically compresses conversation history when approaching limits
- Preserves important context by keeping first and last messages
- Customizable context windows per agent

#### Usage Example:
```go
adaptiveBuilder := context.NewAdaptiveContextBuilder(workspace, agentID)
contextReady, err := adaptiveBuilder.BuildAdaptiveContext(
    agentID,
    sessionKey,
    userMessage,
    history,
    summary,
    maxContextWindow, // e.g., 80% of model's context window
)
```

### 2. Granular Permissions Control

The new permissions system provides fine-grained tool access control:

- Per-agent permissions: Different agents can have access to different tools
- Per-channel permissions: Tools can be restricted based on communication channel
- Centralized management: Permissions can be configured programmatically
- Runtime enforcement: Permission checks occur before tool execution

#### Usage Example:
```go
permissions := permissions.NewToolPermissions()

// Restrict specific tools for an agent on a specific channel
permissions.SetPermission("researcher-agent", "public-chat", "web-search", true)
permissions.SetPermission("researcher-agent", "public-chat", "system-command", false)

// Wrap the tool registry with permission enforcement
guard := permissions.NewToolGuard(permissions, toolRegistry)

// Execute tools with automatic permission checking
result := guard.ExecuteWithContext(ctx, agentID, channel, chatID, toolName, args, callback)
```

## Integration with Existing Code

The modular components maintain backward compatibility with existing code:

- Legacy interfaces preserved where possible
- New features are opt-in
- Gradual migration path from monolithic to modular approach

## Benefits

- **Scalability**: Individual components can be scaled independently
- **Maintainability**: Changes to one component don't affect others
- **Security**: Fine-grained access control for tools
- **Reliability**: Context limits are managed proactively
- **Flexibility**: Easy to extend with new components

## Future Enhancements

- Integration with event-driven frameworks like Beehive
- Advanced permission policies with roles and groups
- Dynamic context optimization based on conversation patterns
- Real-time permission management via configuration files