// Package permissions handles tool access control and permissions for agents and channels.
package permissions

import (
	"context"
	"sync"

	"github.com/sipeed/picoclaw/pkg/tools"
)

// PermissionChecker defines the interface for checking tool permissions.
type PermissionChecker interface {
	HasPermission(agentID, channel, toolName string) bool
	GetAllowedTools(agentID, channel string) []string
}

// ToolPermissions stores the permissions for tools per agent and channel.
type ToolPermissions struct {
	mu         sync.RWMutex
	permissions map[string][]string // agentID:channel -> [allowed_tool_names]
}

// NewToolPermissions creates a new ToolPermissions instance.
func NewToolPermissions() *ToolPermissions {
	return &ToolPermissions{
		permissions: make(map[string][]string),
	}
}

// SetPermission sets the permission for a specific agent/channel combination to use a tool.
func (tp *ToolPermissions) SetPermission(agentID, channel, toolName string, allowed bool) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	key := tp.generateKey(agentID, channel)

	// Get current allowed tools for this agent/channel
	allowedTools := tp.permissions[key]

	// Check if tool is already in the list
	toolExists := false
	for i, t := range allowedTools {
		if t == toolName {
			toolExists = true
			if !allowed {
				// Remove tool from allowed list
				allowedTools = append(allowedTools[:i], allowedTools[i+1:]...)
			}
			break
		}
	}

	// Add tool if allowed and not already in list
	if allowed && !toolExists {
		allowedTools = append(allowedTools, toolName)
	}

	tp.permissions[key] = allowedTools
}

// HasPermission checks if a specific agent/channel combination has permission to use a tool.
func (tp *ToolPermissions) HasPermission(agentID, channel, toolName string) bool {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	key := tp.generateKey(agentID, channel)
	allowedTools := tp.permissions[key]

	for _, tool := range allowedTools {
		if tool == toolName {
			return true
		}
	}

	// If no specific permissions are set, allow by default
	if len(allowedTools) == 0 {
		return true
	}

	return false
}

// GetAllowedTools returns all tools that are allowed for a specific agent/channel combination.
func (tp *ToolPermissions) GetAllowedTools(agentID, channel string) []string {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	key := tp.generateKey(agentID, channel)
	allowedTools := tp.permissions[key]

	// If no specific permissions are set, return nil to indicate all tools are allowed
	if len(allowedTools) == 0 {
		return nil
	}

	result := make([]string, len(allowedTools))
	copy(result, allowedTools)
	return result
}

// SetAgentDefaultPermissions sets default permissions for all channels for a specific agent.
func (tp *ToolPermissions) SetAgentDefaultPermissions(agentID string, allowedTools []string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	for toolName := range tp.getAllTools() {
		allowed := false
		for _, allowedTool := range allowedTools {
			if toolName == allowedTool {
				allowed = true
				break
			}
		}

		key := tp.generateKey(agentID, "*") // Use * to denote all channels
		currentTools := tp.permissions[key]

		// Remove tool if it exists
		for i, t := range currentTools {
			if t == toolName {
				currentTools = append(currentTools[:i], currentTools[i+1:]...)
				break
			}
		}

		// Add tool if allowed
		if allowed {
			currentTools = append(currentTools, toolName)
		}

		tp.permissions[key] = currentTools
	}
}

// SetChannelDefaultPermissions sets default permissions for all agents on a specific channel.
func (tp *ToolPermissions) SetChannelDefaultPermissions(channel string, allowedTools []string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	for toolName := range tp.getAllTools() {
		allowed := false
		for _, allowedTool := range allowedTools {
			if toolName == allowedTool {
				allowed = true
				break
			}
		}

		key := tp.generateKey("*", channel) // Use * to denote all agents
		currentTools := tp.permissions[key]

		// Remove tool if it exists
		for i, t := range currentTools {
			if t == toolName {
				currentTools = append(currentTools[:i], currentTools[i+1:]...)
				break
			}
		}

		// Add tool if allowed
		if allowed {
			currentTools = append(currentTools, toolName)
		}

		tp.permissions[key] = currentTools
	}
}

// getAllTools returns all registered tool names (this would come from the tool registry in practice).
func (tp *ToolPermissions) getAllTools() map[string]bool {
	// In a real implementation, this would query the tool registry
	// For now, return an empty map - this method is mainly a placeholder
	return make(map[string]bool)
}

// generateKey creates a unique key for agent/channel combinations.
func (tp *ToolPermissions) generateKey(agentID, channel string) string {
	return agentID + ":" + channel
}

// ToolGuard wraps a tool registry and enforces permissions.
type ToolGuard struct {
	permissions PermissionChecker
	registry    *tools.ToolRegistry
}

// NewToolGuard creates a new ToolGuard instance.
func NewToolGuard(permissions PermissionChecker, registry *tools.ToolRegistry) *ToolGuard {
	return &ToolGuard{
		permissions: permissions,
		registry:    registry,
	}
}

// ExecuteWithContext executes a tool with permission checking.
func (tg *ToolGuard) ExecuteWithContext(
	ctx context.Context,
	agentID, channel, chatID string,
	toolName string,
	args map[string]interface{},
	asyncCallback func(context.Context, *tools.ToolResult)) *tools.ToolResult {

	// Check permissions
	if !tg.permissions.HasPermission(agentID, channel, toolName) {
		return &tools.ToolResult{
			ForUser: "Permission denied: You don't have access to use this tool.",
			ForLLM:  "Permission denied: Tool access restricted",
			Err:     nil, // Not really an error, just a restriction
			Silent:  false,
		}
	}

	// Use the registry's ExecuteWithContext method which handles all the tool execution details
	return tg.registry.ExecuteWithContext(ctx, toolName, args, channel, chatID, asyncCallback)
}