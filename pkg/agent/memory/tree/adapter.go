// Package tree provides an adapter to integrate tree memory with QuantClaw's existing memory system
package tree

import (
	"fmt"
	"github.com/sipeed/quantclaw/pkg/providers"
	"strings"
	"time"
)

// TreeMemoryAdapter adapts the tree memory system to work with QuantClaw's existing interface
type TreeMemoryAdapter struct {
	treeMemory   *TreeMemory
	sessionNodes map[string]string // Maps session keys to their tree node IDs
}

// NewTreeMemoryAdapter creates a new adapter for tree memory integration
func NewTreeMemoryAdapter(workspace string) *TreeMemoryAdapter {
	return &TreeMemoryAdapter{
		treeMemory:   NewTreeMemory(workspace),
		sessionNodes: make(map[string]string),
	}
}

// StoreSession stores a complete session as a tree structure
func (tma *TreeMemoryAdapter) StoreSession(sessionKey string, messages []providers.Message, summary string) error {
	// Check if we already have a session node for this session
	sessionNodeID, exists := tma.sessionNodes[sessionKey]

	var sessionNode *MemoryNode
	var err error

	if !exists {
		// Create hierarchical structure for this session
		nodeMap, err := tma.treeMemory.CreateHierarchicalStructure("default_user", sessionKey, time.Now())
		if err != nil {
			return err
		}

		sessionNodeID = nodeMap["session"]
		tma.sessionNodes[sessionKey] = sessionNodeID
		sessionNode, _ = tma.treeMemory.GetNode(sessionNodeID)
	} else {
		sessionNode, err = tma.treeMemory.GetNode(sessionNodeID)
		if err != nil {
			return err
		}
	}

	// Create event nodes for each message
	for i, msg := range messages {
		eventTitle := fmt.Sprintf("Message %d (%s)", i+1, msg.Role)
		eventContent := msg.Content

		var tags []string
		switch msg.Role {
		case "user":
			tags = []string{"user_message", "input", sessionKey}
		case "assistant":
			tags = []string{"assistant_response", "output", sessionKey}
		case "system":
			tags = []string{"system_prompt", "context", sessionKey}
		case "tool":
			tags = []string{"tool_result", "execution_result", sessionKey}
		default:
			tags = []string{"message", sessionKey}
		}

		// Add any tool calls to content if present
		if len(msg.ToolCalls) > 0 {
			eventContent += "\n[Tool Calls Made]"
			for _, tc := range msg.ToolCalls {
				eventContent += fmt.Sprintf("\n- Tool: %s", tc.Name)
			}
		}

		// Store the message as an event node
		_, err := tma.treeMemory.Store(NodeTypeEvent, eventTitle, eventContent, sessionNode.ID, tags, nil)
		if err != nil {
			return err
		}
	}

	// If summary is provided, store it as a summary node
	if summary != "" {
		_, err := tma.treeMemory.Store(NodeTypeSummary,
			fmt.Sprintf("Summary for %s", sessionKey),
			summary,
			sessionNode.ID,
			[]string{"summary", sessionKey},
			nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetRelevantHistory retrieves relevant conversation history for a given query
func (tma *TreeMemoryAdapter) GetRelevantHistory(query string, sessionKey string, maxMessages int) ([]providers.Message, error) {
	// Search for relevant memories
	searchQuery := fmt.Sprintf("%s %s", query, sessionKey) // Include session in search
	results, err := tma.treeMemory.Retrieve(searchQuery, "combined")
	if err != nil {
		return nil, err
	}

	var relevantMessages []providers.Message
	messageCount := 0

	// Extract messages from the search results
	for _, result := range results {
		if messageCount >= maxMessages {
			break
		}

		// Parse the content back into a message format
		// This is a simplified parsing - in a real implementation you'd have a more robust way to reconstruct messages
		var role string
		content := result.Node.Content

		// Determine role based on tags
		for _, tag := range result.Node.Tags {
			switch tag {
			case "user_message":
				role = "user"
			case "assistant_response":
				role = "assistant"
			case "system_prompt":
				role = "system"
			case "tool_result":
				role = "tool"
			}
		}

		// If we couldn't determine the role from tags, try to infer from content
		if role == "" {
			if result.Node.Type == NodeTypeSummary {
				role = "system" // Treat summaries as system context
				content = fmt.Sprintf("[Previous Conversation Summary]\n%s", content)
			} else {
				role = "assistant" // Default to assistant
			}
		}

		// Create message object
		msg := providers.Message{
			Role:    role,
			Content: content,
		}

		relevantMessages = append(relevantMessages, msg)
		messageCount++
	}

	// Reverse the order to put more recent messages last (as expected by LLM)
	for i, j := 0, len(relevantMessages)-1; i < j; i, j = i+1, j-1 {
		relevantMessages[i], relevantMessages[j] = relevantMessages[j], relevantMessages[i]
	}

	return relevantMessages, nil
}

// GetSessionSummary retrieves a summary for a specific session
func (tma *TreeMemoryAdapter) GetSessionSummary(sessionKey string) (string, error) {
	results, err := tma.treeMemory.Retrieve(sessionKey, "tags")
	if err != nil {
		return "", err
	}

	for _, result := range results {
		if result.Node.Type == NodeTypeSummary {
			return result.Node.Content, nil
		}
	}

	return "", nil
}

// ExtractAndStoreKnowledge extracts knowledge from conversations and stores it as structured knowledge
func (tma *TreeMemoryAdapter) ExtractAndStoreKnowledge(messages []providers.Message, sessionKey string) error {
	// This would implement knowledge extraction logic
	// For now, we'll identify key topics discussed in the conversation and create topic nodes

	topics := tma.extractTopicsFromMessages(messages)

	for _, topic := range topics {
		// Create a topic node under the root
		_, err := tma.treeMemory.Store(NodeTypeTopic,
			topic,
			fmt.Sprintf("Knowledge about: %s", topic),
			"", // Will be placed under root
			[]string{"topic", "knowledge", topic, sessionKey},
			nil)
		if err != nil {
			// Log error but continue with other topics
			continue
		}
	}

	return nil
}

// extractTopicsFromMessages attempts to identify key topics from messages
func (tma *TreeMemoryAdapter) extractTopicsFromMessages(messages []providers.Message) []string {
	topicMap := make(map[string]bool)

	for _, msg := range messages {
		content := msg.Content

		// Look for common topic indicators in the message
		// This is a simplified approach - real implementation would use NLP
		if msg.Role == "user" {
			// Extract potential topics from user queries
			// For example, if a message mentions specific subjects, technologies, etc.

			// Common topic words/phrases
			commonTopics := []string{
				"technology", "programming", "AI", "machine learning", "computer science",
				"science", "mathematics", "physics", "chemistry", "biology",
				"history", "geography", "literature", "philosophy", "psychology",
				"health", "medicine", "finance", "business", "economics",
				"sports", "music", "art", "cooking", "travel", "news",
			}

			for _, topic := range commonTopics {
				if containsIgnoreCase(content, topic) {
					topicMap[topic] = true
				}
			}
		}
	}

	var topics []string
	for topic := range topicMap {
		topics = append(topics, topic)
	}

	return topics
}

// containsIgnoreCase checks if a string contains another string, ignoring case
func containsIgnoreCase(str, substr string) bool {
	return containsSubstring(strings.ToLower(str), strings.ToLower(substr))
}

// containsSubstring checks if str contains substr
func containsSubstring(str, substr string) bool {
	return strings.Contains(str, substr)
}

// GetMemoryContext builds a comprehensive memory context for the LLM
func (tma *TreeMemoryAdapter) GetMemoryContext(query string, sessionKey string, maxContextLength int) (string, error) {
	var contextBuilder strings.Builder

	// Get recent session-specific memories
	sessionMemories, err := tma.GetRelevantHistory(query, sessionKey, 10) // Up to 10 recent messages
	if err != nil {
		return "", err
	}

	if len(sessionMemories) > 0 {
		contextBuilder.WriteString("## Recent Session Context\n")
		for _, msg := range sessionMemories {
			contextBuilder.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
		}
		contextBuilder.WriteString("\n")
	}

	// Get relevant knowledge from other sessions
	knowledgeQuery := query
	if len(sessionMemories) > 0 {
		// Enhance query with recent context
		for _, msg := range sessionMemories {
			if msg.Role == "user" {
				knowledgeQuery += " " + msg.Content
				break // Just use the most recent user message
			}
		}
	}

	knowledgeResults, err := tma.treeMemory.Retrieve(knowledgeQuery, "semantic")
	if err != nil {
		return "", err
	}

	if len(knowledgeResults) > 0 {
		contextBuilder.WriteString("## Related Knowledge\n")
		for i, result := range knowledgeResults {
			if i >= 5 { // Limit to 5 knowledge items
				break
			}
			contextBuilder.WriteString(fmt.Sprintf("- %s: %s\n", result.Node.Title, result.Node.Content))
		}
		contextBuilder.WriteString("\n")
	}

	// Get long-term knowledge (topics, facts, etc.)
	longTermResults, err := tma.treeMemory.Retrieve(query, "type")
	if err != nil {
		return "", err
	}

	// Filter for long-term knowledge types
	var longTermKnowledge []*SearchResult
	for _, result := range longTermResults {
		switch result.Node.Type {
		case NodeTypeFact, NodeTypeTopic, NodeTypeSummary:
			longTermKnowledge = append(longTermKnowledge, result)
		}
	}

	if len(longTermKnowledge) > 0 {
		contextBuilder.WriteString("## Long-term Knowledge\n")
		for i, result := range longTermKnowledge {
			if i >= 3 { // Limit to 3 long-term items
				break
			}
			contextBuilder.WriteString(fmt.Sprintf("- %s: %s\n", result.Node.Title, result.Node.Content))
		}
		contextBuilder.WriteString("\n")
	}

	contextStr := contextBuilder.String()

	// Truncate if too long (simple character-based truncation)
	if maxContextLength > 0 && len(contextStr) > maxContextLength {
		if maxContextLength < len("...") {
			return "", fmt.Errorf("maxContextLength too small to include even truncation indicator")
		}
		contextStr = contextStr[:maxContextLength-len("...")] + "..."
	}

	return contextStr, nil
}

// CleanupSession removes session-specific nodes when a session ends
func (tma *TreeMemoryAdapter) CleanupSession(sessionKey string) error {
	// This is a simplified implementation
	// In a real system, you might archive the session instead of deleting

	sessionNodeID, exists := tma.sessionNodes[sessionKey]
	if !exists {
		return nil // Nothing to cleanup
	}

	// Delete the session node and its children
	err := tma.treeMemory.DeleteNode(sessionNodeID, true)
	if err != nil {
		return err
	}

	// Remove from our mapping
	delete(tma.sessionNodes, sessionKey)

	return nil
}

// CompactMemory performs memory compaction to manage memory growth
func (tma *TreeMemoryAdapter) CompactMemory(retentionThreshold time.Duration) error {
	// Delegate to the tree memory's compaction
	return tma.treeMemory.CompactMemory(retentionThreshold)
}
