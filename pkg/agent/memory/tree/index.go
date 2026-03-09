// Package tree implements indexing and search capabilities for the tree memory system
package tree

import (
	"math"
	"strings"
	"unicode"
)

// SearchResult represents a search result with relevance score
type SearchResult struct {
	Node  *MemoryNode
	Score float64
}

// IndexManager handles indexing and searching of memory nodes
type IndexManager struct {
	nodes    *NodeManager
	keywords map[string][]string // keyword -> node IDs
	tags     map[string][]string // tag -> node IDs
}

// NewIndexManager creates a new index manager
func NewIndexManager(nodes *NodeManager) *IndexManager {
	return &IndexManager{
		nodes:    nodes,
		keywords: make(map[string][]string),
		tags:     make(map[string][]string),
	}
}

// IndexNode adds a node to the indexes
func (im *IndexManager) IndexNode(node *MemoryNode) {
	im.indexByKeywords(node)
	im.indexByTags(node)
}

// indexByKeywords indexes a node by its keywords extracted from content and title
func (im *IndexManager) indexByKeywords(node *MemoryNode) {
	text := node.Title + " " + node.Content
	keywords := im.extractKeywords(text)

	for _, keyword := range keywords {
		// Normalize keyword
		keyword = strings.ToLower(keyword)

		// Add node ID to this keyword's index
		nodeIDs := im.keywords[keyword]
		nodeIDs = append(nodeIDs, node.ID)
		im.keywords[keyword] = nodeIDs
	}
}

// indexByTags indexes a node by its tags
func (im *IndexManager) indexByTags(node *MemoryNode) {
	for _, tag := range node.Tags {
		tag = strings.ToLower(tag)

		// Add node ID to this tag's index
		nodeIDs := im.tags[tag]
		nodeIDs = append(nodeIDs, node.ID)
		im.tags[tag] = nodeIDs
	}
}

// extractKeywords extracts meaningful keywords from text
func (im *IndexManager) extractKeywords(text string) []string {
	// Convert to lowercase and split by non-alphanumeric characters
	var keywords []string
	var currentKeyword strings.Builder

	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			currentKeyword.WriteRune(r)
		} else {
			if currentKeyword.Len() > 0 {
				keyword := currentKeyword.String()
				// Filter out common stop words and short words
				if !im.isStopWord(keyword) && len(keyword) > 2 {
					keywords = append(keywords, keyword)
				}
				currentKeyword.Reset()
			}
		}
	}

	// Handle the last keyword if text doesn't end with a delimiter
	if currentKeyword.Len() > 0 {
		keyword := currentKeyword.String()
		if !im.isStopWord(keyword) && len(keyword) > 2 {
			keywords = append(keywords, keyword)
		}
	}

	return keywords
}

// isStopWord checks if a word is a common stop word
func (im *IndexManager) isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "up": true, "about": true,
		"into": true, "through": true, "during": true, "before": true, "after": true,
		"above": true, "below": true, "between": true, "among": true, "is": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "must": true, "can": true, "this": true,
		"that": true, "these": true, "those": true, "i": true, "you": true,
		"he": true, "she": true, "it": true, "we": true, "they": true,
		"me": true, "him": true, "her": true, "us": true, "them": true,
	}

	_, exists := stopWords[word]
	return exists
}

// Search performs a keyword search across indexed nodes
func (im *IndexManager) Search(query string) []*SearchResult {
	queryTerms := im.extractKeywords(query)

	// Score each node based on query term matches
	nodeScores := make(map[string]float64)

	for _, term := range queryTerms {
		term = strings.ToLower(term)

		// Search in keywords index
		if nodeIDs, exists := im.keywords[term]; exists {
			for _, nodeID := range nodeIDs {
				nodeScores[nodeID] += 1.0 // Base score for keyword match
			}
		}

		// Also search in tags index for partial matches
		for tag, nodeIDs := range im.tags {
			if strings.Contains(tag, term) {
				for _, nodeID := range nodeIDs {
					nodeScores[nodeID] += 0.5 // Lower score for tag match
				}
			}
		}
	}

	// Convert scores to results and sort by relevance
	var results []*SearchResult
	for nodeID, score := range nodeScores {
		if node, exists := im.nodes.GetNode(nodeID); exists {
			results = append(results, &SearchResult{
				Node:  node,
				Score: score,
			})
		}
	}

	// Sort results by score in descending order
	im.sortResultsByScore(results)

	return results
}

// SearchByTags finds nodes with specific tags
func (im *IndexManager) SearchByTags(tags []string) []*SearchResult {
	var results []*SearchResult

	for _, tag := range tags {
		tag = strings.ToLower(tag)

		if nodeIDs, exists := im.tags[tag]; exists {
			for _, nodeID := range nodeIDs {
				if node, exists := im.nodes.GetNode(nodeID); exists {
					// Calculate score based on tag match
					score := 1.0
					// Boost score if the tag appears in the search query
					for _, qtag := range tags {
						if strings.ToLower(qtag) == tag {
							score *= 2.0
						}
					}

					results = append(results, &SearchResult{
						Node:  node,
						Score: score,
					})
				}
			}
		}
	}

	// Sort results by score in descending order
	im.sortResultsByScore(results)

	return results
}

// SearchByType finds nodes of a specific type
func (im *IndexManager) SearchByType(nodeType NodeType) []*SearchResult {
	nodes := im.nodes.FindNodesByType(nodeType)

	var results []*SearchResult
	for _, node := range nodes {
		results = append(results, &SearchResult{
			Node:  node,
			Score: 1.0, // All nodes of the requested type get equal score
		})
	}

	return results
}

// sortResultsByScore sorts search results by score in descending order
func (im *IndexManager) sortResultsByScore(results []*SearchResult) {
	// Simple bubble sort - for production, consider using sort.Slice
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Score < results[j].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

// FuzzySearch performs a fuzzy search allowing for typos and variations
func (im *IndexManager) FuzzySearch(query string, threshold float64) []*SearchResult {
	queryTerms := im.extractKeywords(query)

	var results []*SearchResult

	// For each indexed keyword, calculate similarity to query terms
	for keyword := range im.keywords {
		bestSimilarity := 0.0

		for _, queryTerm := range queryTerms {
			similarity := im.calculateStringSimilarity(queryTerm, keyword)
			if similarity > bestSimilarity {
				bestSimilarity = similarity
			}
		}

		if bestSimilarity >= threshold {
			// Get all nodes associated with this keyword
			nodeIDs := im.keywords[keyword]
			for _, nodeID := range nodeIDs {
				if node, exists := im.nodes.GetNode(nodeID); exists {
					results = append(results, &SearchResult{
						Node:  node,
						Score: bestSimilarity,
					})
				}
			}
		}
	}

	// Sort results by score
	im.sortResultsByScore(results)

	return results
}

// calculateStringSimilarity calculates similarity between two strings using a simple algorithm
func (im *IndexManager) calculateStringSimilarity(s1, s2 string) float64 {
	// Use a simple character-level similarity algorithm
	// This is a simplified version - in practice, you might use Levenshtein distance
	// or other more sophisticated algorithms

	if s1 == s2 {
		return 1.0
	}

	// Calculate longest common subsequence length relative to total length
	lcsLen := im.longestCommonSubsequence(s1, s2)
	maxLen := len(s1)
	if len(s2) > maxLen {
		maxLen = len(s2)
	}

	if maxLen == 0 {
		return 1.0
	}

	// Use the ratio of LCS to maximum possible length as similarity
	return float64(lcsLen) / float64(maxLen)
}

// longestCommonSubsequence calculates the longest common subsequence length between two strings
func (im *IndexManager) longestCommonSubsequence(s1, s2 string) int {
	m, n := len(s1), len(s2)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if s1[i-1] == s2[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = int(math.Max(float64(dp[i-1][j]), float64(dp[i][j-1])))
			}
		}
	}

	return dp[m][n]
}