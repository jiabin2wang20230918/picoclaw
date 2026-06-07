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

// EnhancedContextBuilder implements a hierarchical context management system
// with on-demand loading, information density optimization, and dynamic layering
type EnhancedContextBuilder struct {
	workspace         string
	skillsLoader      *skills.SkillsLoader
	tools             *tools.ToolRegistry
	activeSkillsCache map[string]string // Cache for currently active skills
	loadedSkills      map[string]bool   // Track which skills are currently loaded
}

func NewEnhancedContextBuilder(workspace string) *EnhancedContextBuilder {
	// builtin skills: skills directory in current project
	// Use the skills/ directory under the current working directory
	wd, _ := os.Getwd()
	builtinSkillsDir := filepath.Join(wd, "skills")
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	return &EnhancedContextBuilder{
		workspace:         workspace,
		skillsLoader:      skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
		activeSkillsCache: make(map[string]string),
		loadedSkills:      make(map[string]bool),
	}
}

// SetToolsRegistry sets the tools registry for dynamic tool summary generation.
func (ecb *EnhancedContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	ecb.tools = registry
}

// GetPersistentLayer returns the constant identity and core rules layer
// This is kept short, hard, and executable
func (ecb *EnhancedContextBuilder) GetPersistentLayer() string {
	workspacePath, _ := filepath.Abs(filepath.Join(ecb.workspace))
	runtimeInfo := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Build tools section dynamically (only descriptions, not full definitions)
	toolsSection := ecb.buildToolsSection()

	return fmt.Sprintf(`# quantclaw 🦞

You are quantclaw, a helpful AI assistant.

## Runtime
%s

## Workspace
Your workspace is at: %s
- Memory: %s/memory/MEMORY.md
- Daily Notes: %s/memory/YYYYMM/YYYYMMDD.md

%s

## Core Rules
1. **ALWAYS use tools** - When you need to perform an action, you MUST call the appropriate tool. Do NOT just say you'll do it.
2. **Be helpful and accurate** - When using tools, briefly explain what you're doing.
3. **Memory** - When something seems memorable, update %s/memory/MEMORY.md`,
		runtimeInfo, workspacePath, workspacePath, workspacePath, toolsSection, workspacePath)
}

// GetRuntimeLayer returns dynamic information that gets injected each round
func (ecb *EnhancedContextBuilder) GetRuntimeLayer(channel, chatID string) string {
	var layer string

	if channel != "" && chatID != "" {
		layer += fmt.Sprintf("\n## Current Session\nChannel: %s\nChat ID: %s", channel, chatID)
	}

	return layer
}

// GetOnDemandLayer returns skills and domain knowledge on demand
// Only returns the skills that are actively being used
func (ecb *EnhancedContextBuilder) GetOnDemandLayer(activeSkills []string) string {
	if len(activeSkills) == 0 {
		return ""
	}

	var parts []string
	for _, skillName := range activeSkills {
		// Load full skill content if not already cached
		if _, exists := ecb.activeSkillsCache[skillName]; !exists {
			content, ok := ecb.skillsLoader.LoadSkill(skillName)
			if ok {
				ecb.activeSkillsCache[skillName] = content
				ecb.loadedSkills[skillName] = true
			}
		}

		// Include the skill in the context
		if content, exists := ecb.activeSkillsCache[skillName]; exists {
			parts = append(parts, fmt.Sprintf("### Skill: %s\n\n%s", skillName, content))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return "# Active Skills\n\n" + strings.Join(parts, "\n\n---\n\n")
}

// GetMemoryLayer returns cross-session memory that's relevant to current context
// This should be read separately when needed, not included in main context
func (ecb *EnhancedContextBuilder) GetMemoryLayer() string {
	// This method provides a way to indicate that memory is available
	// Actual memory content should be retrieved via tools when needed
	return fmt.Sprintf(`# Memory Availability
Relevant memories are available in %s/memory/ directory.
Use read_file tool to access MEMORY.md or specific daily notes as needed.`, ecb.workspace)
}

// BuildSystemPrompt constructs the system prompt using hierarchical layers
// Only includes relevant information based on current context needs
func (ecb *EnhancedContextBuilder) BuildSystemPrompt(channel, chatID string, activeSkills []string) string {
	var parts []string

	// 1. Add persistent layer (always included)
	persistent := ecb.GetPersistentLayer()
	if persistent != "" {
		parts = append(parts, persistent)
	}

	// 2. Add runtime layer (dynamic information)
	runtimeInfo := ecb.GetRuntimeLayer(channel, chatID)
	if runtimeInfo != "" {
		parts = append(parts, runtimeInfo)
	}

	// 3. Add on-demand skills layer (only when specific skills are active)
	onDemandSkills := ecb.GetOnDemandLayer(activeSkills)
	if onDemandSkills != "" {
		parts = append(parts, onDemandSkills)
	}

	// 4. Add bootstrap files
	bootstrapContent := ecb.LoadBootstrapFiles()
	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	// 5. Add memory availability notice
	memoryLayer := ecb.GetMemoryLayer()
	if memoryLayer != "" {
		parts = append(parts, memoryLayer)
	}

	// 6. Add current time at the end (changes every minute, placed last for cache friendliness)
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
	parts = append(parts, fmt.Sprintf("## Current Time\n%s", now))

	// Join with clear separators to maintain information density
	return strings.Join(parts, "\n\n---\n\n")
}

// DetectActiveSkills analyzes current conversation to identify skills that should be loaded
// This is a simple heuristic - in practice, this could use more sophisticated analysis
func (ecb *EnhancedContextBuilder) DetectActiveSkills(messages []providers.Message) []string {
	// Get all available skills
	availableSkills := ecb.skillsLoader.ListSkills()

	// Track which skills are mentioned in recent messages
	mentionedSkills := make(map[string]bool)

	// Check recent messages for skill mentions
	for _, msg := range messages {
		lowerContent := strings.ToLower(msg.Content)
		for _, skill := range availableSkills {
			// Check if skill name or description is mentioned
			if strings.Contains(lowerContent, strings.ToLower(skill.Name)) ||
				strings.Contains(lowerContent, strings.ToLower(skill.Description)) {
				mentionedSkills[skill.Name] = true
			}
		}
	}

	// Convert to slice
	var activeSkills []string
	for skill := range mentionedSkills {
		activeSkills = append(activeSkills, skill)
	}

	return activeSkills
}

func (ecb *EnhancedContextBuilder) buildToolsSection() string {
	if ecb.tools == nil {
		return ""
	}

	summaries := ecb.tools.GetSummaries()
	if len(summaries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	sb.WriteString(
		"**CRITICAL**: You MUST use tools to perform actions. Do NOT pretend to execute commands.\n\n",
	)
	sb.WriteString("You have access to the following tools:\n\n")
	for _, s := range summaries {
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	return sb.String()
}

func (ecb *EnhancedContextBuilder) LoadBootstrapFiles() string {
	var sb strings.Builder
	for _, spec := range resolveBootstrapFiles(ecb.workspace) {
		enabled := spec.Enabled == nil || *spec.Enabled
		if !enabled || strings.TrimSpace(spec.Path) == "" {
			continue
		}

		filePath := filepath.Join(ecb.workspace, spec.Path)
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

// BuildMessages constructs the complete message array with layered approach
func (ecb *EnhancedContextBuilder) BuildMessages(
	history []providers.Message,
	summary string,
	currentMessage string,
	memoryContext string,
	media []string,
	channel, chatID string,
) []providers.Message {
	// Detect which skills are relevant based on conversation history
	activeSkills := ecb.DetectActiveSkills(history)

	// Build system prompt with only relevant layers
	systemPrompt := ecb.BuildSystemPrompt(channel, chatID, activeSkills)

	// Log system prompt summary for debugging (debug mode only)
	logger.DebugCF("agent", "Enhanced system prompt built",
		map[string]any{
			"total_chars":   len(systemPrompt),
			"total_lines":   strings.Count(systemPrompt, "\n") + 1,
			"section_count": strings.Count(systemPrompt, "\n\n---\n\n") + 1,
			"active_skills": len(activeSkills),
			"total_skills":  len(ecb.skillsLoader.ListSkills()),
		})

	// Log preview of system prompt (avoid logging huge content)
	preview := systemPrompt
	if len(preview) > 500 {
		preview = preview[:500] + "... (truncated)"
	}
	logger.DebugCF("agent", "Enhanced system prompt preview",
		map[string]any{
			"preview": preview,
		})

	return assembleProviderMessages(systemPrompt, history, summary, currentMessage, memoryContext)
}

// ClearLoadedSkills clears the cache of currently loaded skills
// Call this periodically to manage memory usage
func (ecb *EnhancedContextBuilder) ClearLoadedSkills() {
	ecb.activeSkillsCache = make(map[string]string)
	ecb.loadedSkills = make(map[string]bool)
}

// AddToolResult adds a tool result to the message sequence
func (ecb *EnhancedContextBuilder) AddToolResult(
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

// AddAssistantMessage adds an assistant message to the message sequence
func (ecb *EnhancedContextBuilder) AddAssistantMessage(
	messages []providers.Message,
	content string,
	toolCalls []map[string]any,
) []providers.Message {
	msg := providers.Message{
		Role:    "assistant",
		Content: content,
	}
	messages = append(messages, msg)
	return messages
}

// GetSkillsInfo returns information about loaded skills.
func (ecb *EnhancedContextBuilder) GetSkillsInfo() map[string]any {
	allSkills := ecb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}
	return map[string]any{
		"total":           len(allSkills),
		"available":       len(allSkills),
		"names":           skillNames,
		"active_cached":   len(ecb.activeSkillsCache),
		"currently_using": len(ecb.loadedSkills),
	}
}
