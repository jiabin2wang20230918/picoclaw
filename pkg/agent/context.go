package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sipeed/quantclaw/pkg/logger"
	"github.com/sipeed/quantclaw/pkg/providers"
	"github.com/sipeed/quantclaw/pkg/skills"
	"github.com/sipeed/quantclaw/pkg/tools"
)

type ContextBuilder struct {
	workspace    string
	skillsLoader *skills.SkillsLoader
	tools        *tools.ToolRegistry // Direct reference to tool registry
}

func getGlobalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".quantclaw")
}

func NewContextBuilder(workspace string) *ContextBuilder {
	// builtin skills: skills directory in current project
	// Use the skills/ directory under the current working directory
	wd, _ := os.Getwd()
	builtinSkillsDir := filepath.Join(wd, "skills")
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	return &ContextBuilder{
		workspace:    workspace,
		skillsLoader: skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
	}
}

// SetToolsRegistry sets the tools registry for dynamic tool summary generation.
func (cb *ContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	cb.tools = registry
}

func (cb *ContextBuilder) getIdentity() string {
	t := time.Now()
	_, offset := t.Zone()
	offsetSign := "+"
	if offset < 0 {
		offsetSign = "-"
		offset = -offset
	}
	offsetHours := offset / 3600
	offsetMins := (offset % 3600) / 60
	utcOffset := fmt.Sprintf("UTC%s%02d:%02d", offsetSign, offsetHours, offsetMins)
	now := t.Format("2006-01-02 15:04 (Monday)") + " " + utcOffset
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	runtime := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Build tools section dynamically
	toolsSection := cb.buildToolsSection()

	return fmt.Sprintf(`# quantclaw 🦞

You are quantclaw, a helpful AI assistant.

## Runtime
%s

## Workspace
Your workspace is at: %s
- Memory: %s/memory/MEMORY.md
- Daily Notes: %s/memory/YYYYMM/YYYYMMDD.md
- Skills: %s/skills/{skill-name}/SKILL.md

%s

## Important Rules

1. **ALWAYS use tools** - When you need to perform an action (schedule reminders, send messages, execute commands, etc.), you MUST call the appropriate tool. Do NOT just say you'll do it or pretend to do it.

2. **Be helpful and accurate** - When using tools, briefly explain what you're doing.

3. **Memory** - When interacting with me if something seems memorable, update %s/memory/MEMORY.md

## Current Time
%s`,
		runtime, workspacePath, workspacePath, workspacePath, workspacePath, toolsSection, workspacePath, now)
}

func (cb *ContextBuilder) buildToolsSection() string {
	if cb.tools == nil {
		return ""
	}

	summaries := cb.tools.GetSummaries()
	if len(summaries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	sb.WriteString(
		"**CRITICAL**: You MUST use tools to perform actions. Do NOT pretend to execute commands or schedule tasks.\n\n",
	)
	sb.WriteString("You have access to the following tools:\n\n")
	for _, s := range summaries {
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	return sb.String()
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
	parts := []string{}

	// Core identity section (Persistent Layer)
	parts = append(parts, cb.getIdentity())

	// Bootstrap files (Persistent Layer)
	bootstrapContent := cb.LoadBootstrapFiles()
	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	// Skills - show summary only (On-Demand Layer concept)
	// Full skill content is loaded via read_file tool only when needed
	skillsSummary := cb.skillsLoader.BuildSkillsSummary()
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Available Skills

The following skills are available for use. Each skill has a name, description, and location.
When you need to use a specific skill, use the 'load_skill' tool to load its detailed content.
This implements on-demand loading - only load skill content when you specifically need to use that skill.

%s

## How to Use Skills

1. **Identify the skill needed** - Based on the task at hand, identify which skill might be useful
2. **Load the skill** - Use the 'load_skill' tool with the skill name to get its detailed content
3. **Apply the skill** - Use the loaded skill content to guide your actions`, skillsSummary))
	}

	// Memory availability notice (Memory Layer)
	parts = append(parts, fmt.Sprintf(`# Memory Access

Cross-session memories are available in %s/memory/ directory.
Use read_file tool to access MEMORY.md or specific daily notes (YYYYMM/YYYYMMDD.md) when relevant.
This keeps memory content separate from system context for better information density.`,
		cb.workspace))

	// Join with "---" separator
	return strings.Join(parts, "\n\n---\n\n")
}

func (cb *ContextBuilder) LoadBootstrapFiles() string {
	var sb strings.Builder
	for _, spec := range resolveBootstrapFiles(cb.workspace) {
		enabled := spec.Enabled == nil || *spec.Enabled
		if !enabled || strings.TrimSpace(spec.Path) == "" {
			continue
		}

		filePath := filepath.Join(cb.workspace, spec.Path)
		if data, err := os.ReadFile(filePath); err == nil {
			fmt.Fprintf(&sb, "## %s\n\n%s\n\n", spec.Path, data)
		} else if spec.Required {
			logger.WarnCF("agent", "Required bootstrap file missing", map[string]any{
				"path": filePath,
			})
		}
	}

	return sb.String()
}

func assembleProviderMessages(
	systemPrompt string,
	history []providers.Message,
	summary string,
	currentMessage string,
	memoryContext string,
) []providers.Message {
	messages := []providers.Message{}

	if summary != "" {
		systemPrompt += "\n\n## Summary of Previous Conversation\n\n" + summary
	}

	history = sanitizeHistoryForProvider(history)

	messages = append(messages, providers.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	if memoryContext != "" {
		messages = append(messages, providers.Message{
			Role:    "user",
			Content: "## Relevant Memories\n\n" + memoryContext,
		})
	}

	messages = append(messages, history...)

	if strings.TrimSpace(currentMessage) != "" {
		messages = append(messages, providers.Message{
			Role:    "user",
			Content: currentMessage,
		})
	}

	return messages
}

// AssembleBaseMessages builds the canonical provider message sequence:
// system prompt, optional relevant memories, sanitized history, and current user input.
// This is the base context assembly mechanism shared by the runtime.
func (cb *ContextBuilder) AssembleBaseMessages(
	history []providers.Message,
	summary string,
	currentMessage string,
	memoryContext string,
	media []string,
	channel, chatID string,
) []providers.Message {
	systemPrompt := cb.BuildSystemPrompt()

	// Add Current Session info if provided
	if channel != "" && chatID != "" {
		systemPrompt += fmt.Sprintf("\n\n## Current Session\nChannel: %s\nChat ID: %s", channel, chatID)
	}

	// Log system prompt summary for debugging (debug mode only)
	logger.DebugCF("agent", "System prompt built",
		map[string]any{
			"total_chars":   len(systemPrompt),
			"total_lines":   strings.Count(systemPrompt, "\n") + 1,
			"section_count": strings.Count(systemPrompt, "\n\n---\n\n") + 1,
		})

	// Log preview of system prompt (avoid logging huge content)
	preview := systemPrompt
	if len(preview) > 500 {
		preview = preview[:500] + "... (truncated)"
	}
	logger.DebugCF("agent", "System prompt preview",
		map[string]any{
			"preview": preview,
		})

	return assembleProviderMessages(systemPrompt, history, summary, currentMessage, memoryContext)
}

// BuildMessages is a compatibility wrapper.
// Runtime call sites should use MessageBuilder, which delegates canonical base
// assembly to AssembleBaseMessages and then applies token-budget policies.
func (cb *ContextBuilder) BuildMessages(
	history []providers.Message,
	summary string,
	currentMessage string,
	memoryContext string,
	media []string,
	channel, chatID string,
) []providers.Message {
	return cb.AssembleBaseMessages(history, summary, currentMessage, memoryContext, media, channel, chatID)
}

func sanitizeHistoryForProvider(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return history
	}

	sanitized := make([]providers.Message, 0, len(history))
	for _, msg := range history {
		switch msg.Role {
		case "tool":
			if len(sanitized) == 0 {
				logger.DebugCF("agent", "Dropping orphaned leading tool message", map[string]any{})
				continue
			}
			last := sanitized[len(sanitized)-1]
			if last.Role == "assistant" && len(last.ToolCalls) > 0 {
				// First tool result after assistant with tool calls
				sanitized = append(sanitized, msg)
			} else if last.Role == "tool" {
				// Consecutive tool result - walk back to verify valid tool call chain
				valid := false
				for k := len(sanitized) - 1; k >= 0; k-- {
					if sanitized[k].Role == "assistant" {
						valid = len(sanitized[k].ToolCalls) > 0
						break
					}
					if sanitized[k].Role != "tool" {
						break
					}
				}
				if valid {
					sanitized = append(sanitized, msg)
				} else {
					logger.DebugCF("agent", "Dropping orphaned tool message (no valid assistant in chain)", map[string]any{})
					continue
				}
			} else {
				logger.DebugCF("agent", "Dropping orphaned tool message", map[string]any{})
				continue
			}

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				if len(sanitized) == 0 {
					logger.DebugCF("agent", "Dropping assistant tool-call turn at history start", map[string]any{})
					continue
				}
				prev := sanitized[len(sanitized)-1]
				if prev.Role != "user" && prev.Role != "tool" {
					logger.DebugCF(
						"agent",
						"Dropping assistant tool-call turn with invalid predecessor",
						map[string]any{"prev_role": prev.Role},
					)
					continue
				}
			}
			sanitized = append(sanitized, msg)

		default:
			sanitized = append(sanitized, msg)
		}
	}

	return sanitized
}

func (cb *ContextBuilder) AddToolResult(
	messages []providers.Message,
	toolCallID, toolName, result string,
) []providers.Message {
	messages = append(messages, providers.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	})
	return messages
}

func (cb *ContextBuilder) AddAssistantMessage(
	messages []providers.Message,
	content string,
	toolCalls []map[string]any,
) []providers.Message {
	msg := providers.Message{
		Role:    "assistant",
		Content: content,
	}
	// Always add assistant message, whether or not it has tool calls
	messages = append(messages, msg)
	return messages
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]any {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}
	return map[string]any{
		"total":     len(allSkills),
		"available": len(allSkills),
		"names":     skillNames,
	}
}

// BuildMessagesOptimized constructs messages with optimized information density
// This method intelligently manages context size and relevance
func (cb *ContextBuilder) BuildMessagesOptimized(
	history []providers.Message,
	summary string,
	currentMessage string,
	memoryContext string,
	media []string,
	channel, chatID string,
	tokenBudget int, // Maximum token budget for the context
) []providers.Message {

	// Build the initial message sequence
	messages := cb.AssembleBaseMessages(history, summary, currentMessage, memoryContext, media, channel, chatID)

	// If there's no token budget constraint, return as-is
	if tokenBudget <= 0 {
		return messages
	}

	// Apply information density optimization based on token budget
	return cb.optimizeForTokenBudget(messages, tokenBudget)
}

// optimizeForTokenBudget optimizes messages to fit within the specified token budget
// This is a simplified implementation - in practice, you'd use actual token counting
func (cb *ContextBuilder) optimizeForTokenBudget(messages []providers.Message, tokenBudget int) []providers.Message {
	// This is a simple implementation that just counts characters as proxy for tokens
	// In practice, you'd want to use a proper token counter

	// Calculate current message length
	totalChars := 0
	for _, msg := range messages {
		totalChars += len(msg.Content)
	}

	// If we're already under budget, return as-is
	if totalChars <= tokenBudget {
		return messages
	}

	// We need to optimize. Start by preserving system message and current user message
	optimizedMessages := []providers.Message{}
	var systemMsg *providers.Message
	var userMsg *providers.Message
	var assistantAndToolMsgs []providers.Message

	for i, msg := range messages {
		if msg.Role == "system" {
			systemMsg = &messages[i]
		} else if msg.Role == "user" && i == len(messages)-1 { // Last user message is the current one
			userMsg = &messages[i]
		} else {
			assistantAndToolMsgs = append(assistantAndToolMsgs, messages[i])
		}
	}

	if systemMsg != nil {
		optimizedMessages = append(optimizedMessages, *systemMsg)
	}

	// Add memory context if present (this is usually important)
	for _, msg := range assistantAndToolMsgs {
		if strings.Contains(msg.Content, "## Relevant Memories") {
			optimizedMessages = append(optimizedMessages, msg)
			break // Only add memory context once
		}
	}

	// Add history with smart truncation if needed
	remainingBudget := tokenBudget
	if systemMsg != nil {
		remainingBudget -= len(systemMsg.Content)
	}
	if userMsg != nil {
		remainingBudget -= len(userMsg.Content)
	}

	// Add history messages (excluding memory context) up to budget
	historyAdded := 0
	for i := len(assistantAndToolMsgs) - 1; i >= 0; i-- { // Start from most recent
		msg := assistantAndToolMsgs[i]
		// Skip memory context as it's already added
		if !strings.Contains(msg.Content, "## Relevant Memories") {
			if len(msg.Content) <= remainingBudget {
				// Prepend to maintain order (most recent first)
				newOptimized := []providers.Message{msg}
				newOptimized = append(newOptimized, optimizedMessages[1:]...)                          // Skip system message
				optimizedMessages = append([]providers.Message{optimizedMessages[0]}, newOptimized...) // Re-add system at start
				remainingBudget -= len(msg.Content)
				historyAdded++

				// Limit the number of historical messages for better information density
				if historyAdded >= 5 { // Limit to 5 recent exchanges for better density
					break
				}
			}
		}
	}

	// Finally add the current user message
	if userMsg != nil {
		optimizedMessages = append(optimizedMessages, *userMsg)
	}

	return optimizedMessages
}
