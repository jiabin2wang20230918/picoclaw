package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// SkillLoaderTool is a specialized tool that allows the agent to dynamically load skills when needed
// This supports the on-demand loading concept by only loading skill content when explicitly requested
type SkillLoaderTool struct {
	skillsLoader *skills.SkillsLoader
}

// SkillLoaderArgs represents the arguments for the skill loader tool
type SkillLoaderArgs struct {
	SkillName string `json:"skill_name"`
}

// NewSkillLoaderTool creates a new instance of the skill loader tool
func NewSkillLoaderTool(skillsLoader *skills.SkillsLoader) *SkillLoaderTool {
	return &SkillLoaderTool{
		skillsLoader: skillsLoader,
	}
}

// Name returns the name of the tool
func (slt *SkillLoaderTool) Name() string {
	return "load_skill"
}

// Description returns the description of the tool
func (slt *SkillLoaderTool) Description() string {
	return "Load a specific skill definition by name when needed. This supports on-demand loading to maintain optimal information density."
}

// Schema returns the JSON schema for the tool arguments
func (slt *SkillLoaderTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": slt.Description(),
		"properties": map[string]interface{}{
			"skill_name": map[string]interface{}{
				"type":        "string",
				"description": "The name of the skill to load",
			},
		},
		"required": []string{"skill_name"},
	}
}

// Parameters returns the JSON schema for the tool arguments (implementing tools.Tool interface)
func (slt *SkillLoaderTool) Parameters() map[string]interface{} {
	return slt.Schema()["properties"].(map[string]interface{})
}

// Execute runs the skill loader tool
func (slt *SkillLoaderTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	var parsedArgs SkillLoaderArgs

	if skillName, ok := args["skill_name"].(string); ok {
		parsedArgs.SkillName = skillName
	} else {
		return tools.ErrorResult("Missing required argument: skill_name")
	}

	// Attempt to load the skill
	content, ok := slt.skillsLoader.LoadSkill(parsedArgs.SkillName)
	if !ok {
		result := tools.ErrorResult(fmt.Sprintf("Skill '%s' not found", parsedArgs.SkillName))
		result.ForUser = fmt.Sprintf("Could not find skill named '%s'. Available skills can be seen in the system prompt.", parsedArgs.SkillName)
		return result
	}

	// Return the skill content for both LLM and user
	result := tools.NewToolResult(fmt.Sprintf("Successfully loaded skill '%s':\n\n%s", parsedArgs.SkillName, content))
	result.ForUser = fmt.Sprintf("Loaded skill '%s' with %d characters of content", parsedArgs.SkillName, len(content))
	return result
}

// DetectSkillIntent analyzes messages to determine if a specific skill is being referenced
// This helps the system proactively suggest loading relevant skills
func DetectSkillIntent(messages []providers.Message, availableSkills []skills.SkillInfo) []string {
	var intendedSkills []string

	// Create a map of skill names for quick lookup
	skillMap := make(map[string]skills.SkillInfo)
	for _, skill := range availableSkills {
		skillMap[strings.ToLower(skill.Name)] = skill
		// Also add the description as a potential match term
		terms := strings.Fields(strings.ToLower(skill.Description))
		for _, term := range terms {
			if len(term) > 3 { // Only consider terms longer than 3 chars
				skillMap[term] = skill
			}
		}
	}

	// Analyze the last few messages for skill references
	for _, msg := range messages {
		content := strings.ToLower(msg.Content)

		// Look for direct skill name matches
		for skillName, skillInfo := range skillMap {
			if strings.Contains(content, strings.ToLower(skillName)) {
				intendedSkills = append(intendedSkills, skillInfo.Name)
			}
		}
	}

	// Remove duplicates
	uniqueSkills := make(map[string]bool)
	var result []string
	for _, skill := range intendedSkills {
		if !uniqueSkills[skill] {
			uniqueSkills[skill] = true
			result = append(result, skill)
		}
	}

	return result
}