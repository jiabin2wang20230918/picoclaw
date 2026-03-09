// Package tree provides a hierarchical tree-like memory system for PicoClaw.
// This system organizes memory in a tree structure with multiple layers of abstraction,
// enabling efficient storage, retrieval, and contextual understanding of information.
package tree

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// TreeMemory represents the complete tree-based memory system
type TreeMemory struct {
	nodes      *NodeManager
	index      *IndexManager
	vector     *VectorStore
	workspace  string
}

// NewTreeMemory creates a new tree-based memory system
func NewTreeMemory(workspace string) *TreeMemory {
	nodes := NewNodeManager(workspace)
	index := NewIndexManager(nodes)
	vector := NewVectorStore(nodes)

	return &TreeMemory{
		nodes:     nodes,
		index:     index,
		vector:    vector,
		workspace: workspace,
	}
}

// Store stores information in the tree memory with context and metadata
func (tm *TreeMemory) Store(contentType NodeType, title, content string, parentID string, tags []string, metadata map[string]interface{}) (*MemoryNode, error) {
	node, err := tm.nodes.CreateNode(contentType, title, content, parentID, tags)
	if err != nil {
		return nil, err
	}

	// Add to indexes
	tm.index.IndexNode(node)

	// Add to vector store
	fullText := title + " " + content
	tm.vector.AddOrUpdateEmbedding(node.ID, fullText)

	// Update metadata if provided
	if metadata != nil {
		for k, v := range metadata {
			node.Metadata[k] = v
		}
	}

	return node, nil
}

// Retrieve retrieves information from memory using various search methods
func (tm *TreeMemory) Retrieve(query string, searchType string) ([]*SearchResult, error) {
	switch strings.ToLower(searchType) {
	case "keyword":
		return tm.index.Search(query), nil
	case "semantic", "similarity":
		return tm.vector.SearchBySimilarity(query, 10), nil // Top 10 results
	case "tags":
		tags := strings.Fields(query) // Split query into individual tags
		return tm.index.SearchByTags(tags), nil
	case "type":
		nodeType := NodeType(query)
		return tm.index.SearchByType(nodeType), nil
	case "fuzzy":
		return tm.index.FuzzySearch(query, 0.6), nil // Threshold of 0.6
	default:
		// Default to combined search: keyword + semantic
		keywordResults := tm.index.Search(query)
		semanticResults := tm.vector.SearchBySimilarity(query, 10)

		// Combine and deduplicate results
		combinedResults := tm.combineResults(keywordResults, semanticResults)

		// Sort by composite score
		sort.Slice(combinedResults, func(i, j int) bool {
			return combinedResults[i].Score > combinedResults[j].Score
		})

		return combinedResults, nil
	}
}

// combineResults merges keyword and semantic search results
func (tm *TreeMemory) combineResults(keywordResults, semanticResults []*SearchResult) []*SearchResult {
	// Create a map to avoid duplicate nodes
	resultMap := make(map[string]*SearchResult)

	// Add keyword results with boosted scores
	for _, result := range keywordResults {
		id := result.Node.ID
		boostedScore := result.Score * 1.2 // Boost keyword match slightly
		resultMap[id] = &SearchResult{
			Node:  result.Node,
			Score: boostedScore,
		}
	}

	// Add semantic results, boosting existing ones or adding new ones
	for _, result := range semanticResults {
		id := result.Node.ID
		if existing, exists := resultMap[id]; exists {
			// Combine scores for existing results
			existing.Score = (existing.Score + result.Score) / 2.0
		} else {
			// Add new semantic result
			resultMap[id] = result
		}
	}

	// Convert map back to slice
	var results []*SearchResult
	for _, result := range resultMap {
		results = append(results, result)
	}

	return results
}

// GetNode retrieves a specific node by ID
func (tm *TreeMemory) GetNode(id string) (*MemoryNode, error) {
	node, exists := tm.nodes.GetNode(id)
	if !exists {
		return nil, fmt.Errorf("node with ID %s not found", id)
	}
	return node, nil
}

// UpdateNode updates an existing node
func (tm *TreeMemory) UpdateNode(id, title, content string, tags []string) error {
	err := tm.nodes.UpdateNode(id, title, content, tags)
	if err != nil {
		return err
	}

	// Re-index the updated node
	if node, exists := tm.nodes.GetNode(id); exists {
		tm.index.IndexNode(node)
		fullText := node.Title + " " + node.Content
		tm.vector.AddOrUpdateEmbedding(node.ID, fullText)
	}

	return nil
}

// DeleteNode removes a node from memory
func (tm *TreeMemory) DeleteNode(id string, recursive bool) error {
	return tm.nodes.DeleteNode(id, recursive)
}

// GetSubtree returns all nodes in a subtree
func (tm *TreeMemory) GetSubtree(rootID string) ([]*MemoryNode, error) {
	return tm.nodes.GetSubtree(rootID)
}

// GetPathToRoot returns the path from a node to the root
func (tm *TreeMemory) GetPathToRoot(nodeID string) ([]*MemoryNode, error) {
	return tm.nodes.GetPathToRoot(nodeID)
}

// CreateHierarchicalStructure creates a hierarchical structure for organizing memories
// Creates a root node, then topic nodes, then session nodes, etc.
func (tm *TreeMemory) CreateHierarchicalStructure(userID, sessionID string, timestamp time.Time) (map[string]string, error) {
	// Create temporal nodes (year/month/day)
	year := fmt.Sprintf("%d", timestamp.Year())
	month := fmt.Sprintf("%d-%02d", timestamp.Year(), timestamp.Month())
	day := fmt.Sprintf("%d-%02d-%02d", timestamp.Year(), timestamp.Month(), timestamp.Day())

	// Root node for the user
	rootNode, err := tm.Store(NodeTypeRoot, fmt.Sprintf("User Memory - %s", userID),
		fmt.Sprintf("Root memory node for user %s", userID), "",
		[]string{"user", userID}, nil)
	if err != nil {
		return nil, err
	}

	// Year node
	yearNode, err := tm.Store(NodeTypeTemporal, year,
		fmt.Sprintf("Memories from %s", year), rootNode.ID,
		[]string{"year", year}, nil)
	if err != nil {
		return nil, err
	}

	// Month node
	monthNode, err := tm.Store(NodeTypeTemporal, month,
		fmt.Sprintf("Memories from %s", month), yearNode.ID,
		[]string{"month", month}, nil)
	if err != nil {
		return nil, err
	}

	// Day node
	dayNode, err := tm.Store(NodeTypeTemporal, day,
		fmt.Sprintf("Memories from %s", day), monthNode.ID,
		[]string{"day", day}, nil)
	if err != nil {
		return nil, err
	}

	// Session node
	sessionNode, err := tm.Store(NodeTypeSession, fmt.Sprintf("Session %s", sessionID),
		fmt.Sprintf("Conversation session %s", sessionID), dayNode.ID,
		[]string{"session", sessionID}, nil)
	if err != nil {
		return nil, err
	}

	// Return map of node types to their IDs for easy reference
	nodeMap := map[string]string{
		"root":    rootNode.ID,
		"year":    yearNode.ID,
		"month":   monthNode.ID,
		"day":     dayNode.ID,
		"session": sessionNode.ID,
	}

	return nodeMap, nil
}

// CompactMemory performs memory compaction to manage growth
// This moves less important memories to deeper nodes and removes obsolete ones
func (tm *TreeMemory) CompactMemory(retentionThreshold time.Duration) error {
	// For now, this is a placeholder - in a real implementation,
	// this would move older memories to archived nodes or remove them

	// This could involve:
	// 1. Identifying nodes older than retentionThreshold
	// 2. Moving important nodes to higher retention levels
	// 3. Archiving or deleting less important nodes

	return nil
}

// GetMemoryContext builds a context string from relevant memories
// This is used to provide memory context to the LLM
func (tm *TreeMemory) GetMemoryContext(query string, maxResults int) (string, error) {
	results, err := tm.Retrieve(query, "combined")
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return "", nil
	}

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString("## Relevant Past Memories\n\n")

	for i, result := range results {
		contextBuilder.WriteString(fmt.Sprintf("### Memory %d: %s\n", i+1, result.Node.Title))
		contextBuilder.WriteString(result.Node.Content)
		contextBuilder.WriteString("\n\n")
	}

	return contextBuilder.String(), nil
}

// ExtractKnowledge extracts structured knowledge from conversation histories
func (tm *TreeMemory) ExtractKnowledge(content string, context map[string]interface{}) error {
	// This is a placeholder for knowledge extraction logic
	// In a real implementation, this would use NLP to identify facts, entities, and relationships

	// Example: Extract facts and store them as fact nodes
	// Example: Identify entities and store them as entity nodes
	// Example: Recognize relationships and store them appropriately

	return nil
}