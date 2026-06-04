package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/quantclaw/pkg/providers"
)

// SystemRule defines a rule that handles deterministic logic externally
type SystemRule interface {
	// Name returns the name of the rule
	Name() string

	// AppliesTo checks if the rule applies to the given message
	AppliesTo(message string, role string) bool

	// Process applies the rule logic and potentially modifies the input/output
	Process(ctx context.Context, message string, role string) (processedMessage string, shouldContinue bool, err error)
}

// RuleProcessor manages and applies system rules
type RuleProcessor struct {
	rules []SystemRule
}

// NewRuleProcessor creates a new rule processor
func NewRuleProcessor() *RuleProcessor {
	return &RuleProcessor{
		rules: []SystemRule{},
	}
}

// AddRule adds a system rule to the processor
func (rp *RuleProcessor) AddRule(rule SystemRule) {
	rp.rules = append(rp.rules, rule)
}

// Process applies all applicable rules to the given message
func (rp *RuleProcessor) Process(ctx context.Context, message string, role string) (string, bool, error) {
	processedMessage := message
	shouldContinue := true

	for _, rule := range rp.rules {
		if rule.AppliesTo(processedMessage, role) {
			var err error
			processedMessage, shouldContinue, err = rule.Process(ctx, processedMessage, role)
			if err != nil {
				return processedMessage, shouldContinue, err
			}

			// If a rule indicates we shouldn't continue, return early
			if !shouldContinue {
				return processedMessage, shouldContinue, nil
			}
		}
	}

	return processedMessage, shouldContinue, nil
}

// ToolExecutionRule handles tool execution logic externally
type ToolExecutionRule struct{}

func (r *ToolExecutionRule) Name() string {
	return "tool_execution_rule"
}

func (r *ToolExecutionRule) AppliesTo(message string, role string) bool {
	// This rule applies to assistant messages that contain tool calls
	return role == "assistant" && strings.Contains(strings.ToLower(message), "call:")
}

func (r *ToolExecutionRule) Process(ctx context.Context, message string, role string) (string, bool, error) {
	// This rule would handle tool execution externally
	// For now, just return the message as-is with continue = true
	return message, true, nil
}

// SecurityFilterRule filters potentially harmful content
type SecurityFilterRule struct {
	blockedPatterns []string
}

func NewSecurityFilterRule(blockedPatterns []string) *SecurityFilterRule {
	return &SecurityFilterRule{
		blockedPatterns: blockedPatterns,
	}
}

func (r *SecurityFilterRule) Name() string {
	return "security_filter_rule"
}

func (r *SecurityFilterRule) AppliesTo(message string, role string) bool {
	// Apply security filtering to user and assistant messages
	return (role == "user" || role == "assistant") && r.containsBlockedPattern(message)
}

func (r *SecurityFilterRule) containsBlockedPattern(text string) bool {
	textLower := strings.ToLower(text)
	for _, pattern := range r.blockedPatterns {
		if strings.Contains(textLower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func (r *SecurityFilterRule) Process(ctx context.Context, message string, role string) (string, bool, error) {
	// In a real implementation, this would block or sanitize harmful content
	// For now, we'll return an error indicating the message contains blocked content
	return "", false, fmt.Errorf("message contains blocked content")
}

// ContextEnforcementRule enforces context-related rules
type ContextEnforcementRule struct {
	maxContextLength int
}

func NewContextEnforcementRule(maxContextLength int) *ContextEnforcementRule {
	return &ContextEnforcementRule{
		maxContextLength: maxContextLength,
	}
}

func (r *ContextEnforcementRule) Name() string {
	return "context_enforcement_rule"
}

func (r *ContextEnforcementRule) AppliesTo(message string, role string) bool {
	// Apply context enforcement to system messages
	return role == "system" && len(message) > r.maxContextLength
}

func (r *ContextEnforcementRule) Process(ctx context.Context, message string, role string) (string, bool, error) {
	// Truncate the message to the maximum allowed length
	if len(message) > r.maxContextLength {
		truncated := message[:r.maxContextLength]
		return truncated, true, nil
	}
	return message, true, nil
}

// applyRulesToMessages applies system rules to a set of messages
func (rp *RuleProcessor) applyRulesToMessages(ctx context.Context, messages []providers.Message) ([]providers.Message, error) {
	processedMessages := make([]providers.Message, len(messages))

	for i, msg := range messages {
		processedContent, shouldContinue, err := rp.Process(ctx, msg.Content, msg.Role)
		if err != nil {
			return nil, err
		}

		// If the rule says we shouldn't continue, handle appropriately
		if !shouldContinue {
			// In this case, we might want to return early or handle differently
			// For now, we'll continue processing but log the event
		}

		processedMessages[i] = providers.Message{
			Role:       msg.Role,
			Content:    processedContent,
			ToolCalls:  msg.ToolCalls,
			ToolCallID: msg.ToolCallID,
		}
	}

	return processedMessages, nil
}
