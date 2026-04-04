// Package memory implements the sediment for Memory-Like-A-Tree architecture
package memory

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Sedimenter identifies important information for long-term preservation
type Sedimenter struct {
	db             *sql.DB
	confidenceMgr  *ConfidenceTracker
	indexer        *Indexer
}

// NewSedimenter creates a new sediment processor
func NewSedimenter(db *sql.DB, confidenceMgr *ConfidenceTracker, indexer *Indexer) *Sedimenter {
	return &Sedimenter{
		db:            db,
		confidenceMgr: confidenceMgr,
		indexer:       indexer,
	}
}

// SedimentKnowledge processes content to identify and preserve important knowledge
func (sed *Sedimenter) SedimentKnowledge(content string, sourceKey string) error {
	return sed.SedimentKnowledgeWithType(content, sourceKey, MemoryTypeFact)
}

// SedimentKnowledgeWithType preserves extracted knowledge with an explicit top-level memory type.
func (sed *Sedimenter) SedimentKnowledgeWithType(content string, sourceKey string, memoryType MemoryType) error {
	// Extract different types of knowledge from content
	knowledgePieces := sed.extractKnowledgePieces(content, sourceKey, memoryType)

	for _, piece := range knowledgePieces {
		// Create a key for the knowledge piece
		key := fmt.Sprintf("knowledge:%s:%d", piece.Type, time.Now().UnixNano())

		// Save the knowledge piece
		core := &MemoryCore{db: sed.db}
		if err := core.SaveRecord(MemoryRecord{
			Key:       key,
			Content:   piece.Content,
			Type:      piece.Type,
			SourceKey: sourceKey,
			Tags:      piece.Tags,
			Reason:    piece.Reason,
		}); err != nil {
			continue // Continue even if one piece fails
		}

		// Set high confidence for extracted knowledge
		if err := sed.confidenceMgr.UpdateConfidence(key, piece.Confidence, "sedimentation"); err != nil {
			continue
		}

		// Create relationship with source
		if err := sed.indexer.createRelationship(sourceKey, key, "contains_knowledge"); err != nil {
			continue
		}
	}

	return nil
}

// KnowledgePiece represents a piece of extracted knowledge
type KnowledgePiece struct {
	Type       MemoryType
	Content    string  // The actual knowledge content
	Confidence float64 // Initial confidence in this knowledge
	Source     string  // Where the knowledge came from
	Tags       []string // Associated tags
	Reason     string
}

// extractKnowledgePieces extracts different types of knowledge from content
func (sed *Sedimenter) extractKnowledgePieces(content, source string, memoryType MemoryType) []KnowledgePiece {
	var pieces []KnowledgePiece

	// Extract facts
	facts := sed.extractFacts(content)
	for _, fact := range facts {
		pieces = append(pieces, KnowledgePiece{
			Type:       MemoryTypeFact,
			Content:    fact,
			Confidence: 0.8,
			Source:     source,
			Tags:       []string{"fact", "information"},
			Reason:     "fact_extraction",
		})
	}

	preferences := sed.extractPreferences(content)
	for _, pref := range preferences {
		pieces = append(pieces, KnowledgePiece{
			Type:       MemoryTypePreference,
			Content:    pref,
			Confidence: 0.82,
			Source:     source,
			Tags:       []string{"preference"},
			Reason:     "preference_extraction",
		})
	}

	// Extract concepts
	concepts := sed.extractConcepts(content)
	for _, concept := range concepts {
		pieces = append(pieces, KnowledgePiece{
			Type:       normalizeMemoryType(memoryType),
			Content:    concept,
			Confidence: 0.7,
			Source:     source,
			Tags:       []string{"concept", "idea", string(normalizeMemoryType(memoryType))},
			Reason:     "concept_extraction",
		})
	}

	// Extract relationships
	relationships := sed.extractRelationships(content)
	for _, rel := range relationships {
		pieces = append(pieces, KnowledgePiece{
			Type:       MemoryTypeFact,
			Content:    rel,
			Confidence: 0.75,
			Source:     source,
			Tags:       []string{"relationship", "connection"},
			Reason:     "relationship_extraction",
		})
	}

	// Extract procedures/ways of doing things
	procedures := sed.extractProcedures(content)
	for _, proc := range procedures {
		pieces = append(pieces, KnowledgePiece{
			Type:       normalizeMemoryType(memoryType),
			Content:    proc,
			Confidence: 0.75,
			Source:     source,
			Tags:       []string{"procedure", "howto", string(normalizeMemoryType(memoryType))},
			Reason:     "procedure_extraction",
		})
	}

	if len(pieces) == 0 && strings.TrimSpace(content) != "" {
		pieces = append(pieces, KnowledgePiece{
			Type:       normalizeMemoryType(memoryType),
			Content:    strings.TrimSpace(content),
			Confidence: 0.7,
			Source:     source,
			Tags:       []string{string(normalizeMemoryType(memoryType))},
			Reason:     "raw_capture",
		})
	}

	return pieces
}

func (sed *Sedimenter) extractPreferences(content string) []string {
	var preferences []string
	re := regexp.MustCompile(`(?i)\b(i|user)\s+(prefer|prefers|like|likes|love|loves|dislike|dislikes|hate|hates|want|wants)\b[^.!?\n]*`)
	matches := re.FindAllString(content, -1)
	for _, match := range matches {
		match = strings.TrimSpace(match)
		if len(match) >= 12 {
			preferences = append(preferences, match)
		}
	}
	return preferences
}

// extractFacts extracts factual information from content
func (sed *Sedimenter) extractFacts(content string) []string {
	var facts []string

	// Look for statements that might be facts
	// Pattern: "X is Y", "X are Y", "X was Y", etc.
	re := regexp.MustCompile(`(?i)\b([^.!?]*?(?:is|are|was|were|has|have|had|can|will|would|should)\s+[^.!?]*?)\.`)
	matches := re.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		if len(match) > 1 {
			fact := strings.TrimSpace(match[1])
			if len(fact) > 10 { // Skip very short fragments
				facts = append(facts, fact)
			}
		}
	}

	// Also look for explicit fact statements
	explicitFacts := sed.extractExplicitFacts(content)
	facts = append(facts, explicitFacts...)

	return facts
}

// extractExplicitFacts looks for content that is explicitly marked as facts
func (sed *Sedimenter) extractExplicitFacts(content string) []string {
	var facts []string

	// Look for bullet points, numbered lists, or other structured facts
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Look for bullet points
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			item := strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* ")
			if len(item) > 10 { // Ensure it's substantial
				facts = append(facts, item)
			}
		}

		// Look for numbered items
		if matched, _ := regexp.MatchString(`^\d+\.\s`, line); matched {
			item := regexp.MustCompile(`^\d+\.\s`).ReplaceAllString(line, "")
			if len(item) > 10 { // Ensure it's substantial
				facts = append(facts, item)
			}
		}
	}

	return facts
}

// extractConcepts extracts conceptual information
func (sed *Sedimenter) extractConcepts(content string) []string {
	var concepts []string

	// Look for technical terms, proper nouns, or important concepts
	// This is a simplified implementation - in reality, you'd use NLP
	words := strings.Fields(content)
	conceptMap := make(map[string]bool)

	for _, word := range words {
		word = strings.Trim(word, ".,!?;:\"'()")

		// Look for capitalized words that might be concepts
		if len(word) > 3 && sed.isLikelyConcept(word) {
			conceptMap[strings.ToLower(word)] = true
		}
	}

	for concept := range conceptMap {
		concepts = append(concepts, concept)
	}

	return concepts
}

// isLikelyConcept determines if a word is likely a concept worth preserving
func (sed *Sedimenter) isLikelyConcept(word string) bool {
	// Skip common words
	if sed.isCommonWord(word) {
		return false
	}

	// Skip very short words
	if len(word) < 4 {
		return false
	}

	// Look for words that start with capital letters (proper nouns)
	if word[0] >= 'A' && word[0] <= 'Z' {
		return true
	}

	// Check if it's a technical term (contains uppercase in middle, etc.)
	hasCapitalInMiddle := false
	for i, char := range word {
		if i > 0 && i < len(word)-1 && char >= 'A' && char <= 'Z' {
			hasCapitalInMiddle = true
			break
		}
	}

	if hasCapitalInMiddle {
		return true
	}

	return false
}

// isCommonWord checks if a word is common
func (sed *Sedimenter) isCommonWord(word string) bool {
	commonWords := map[string]bool{
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
		"go": true, "goes": true, "going": true, "went": true, "gone": true,
		"also": true, "well": true, "really": true, "very": true, "quite": true,
	}

	return commonWords[strings.ToLower(word)]
}

// extractRelationships extracts relationship information
func (sed *Sedimenter) extractRelationships(content string) []string {
	var relationships []string

	// Look for patterns indicating relationships
	// e.g., "X connects to Y", "X relates to Y", "X depends on Y", etc.
	relationshipPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(\w+)\s+(?:connects to|relates to|interacts with|depends on|requires|associated with)\s+(\w+)`),
		regexp.MustCompile(`(?i)(\w+)\s+and\s+(\w+)\s+(?:are related|have a relationship|interact)`),
		regexp.MustCompile(`(?i)(\w+)\s+is part of\s+(\w+)`),
		regexp.MustCompile(`(?i)(\w+)\s+causes\s+(\w+)`),
		regexp.MustCompile(`(?i)(\w+)\s+similar to\s+(\w+)`),
	}

	for _, pattern := range relationshipPatterns {
		matches := pattern.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) >= 3 {
				rel := fmt.Sprintf("%s -> %s (%s)", match[1], match[2], strings.TrimSpace(match[0]))
				relationships = append(relationships, rel)
			}
		}
	}

	return relationships
}

// extractProcedures extracts procedural information
func (sed *Sedimenter) extractProcedures(content string) []string {
	var procedures []string

	// Look for procedural content like "to do X, first Y, then Z"
	proceduresPattern := regexp.MustCompile(`(?i)(?:to\s+\w+|in order to\s+\w+|how to\s+\w+)[^.!?]*?\.`)
	matches := proceduresPattern.FindAllString(content, -1)

	for _, match := range matches {
		if len(match) > 20 { // Ensure it's substantial procedure info
			procedures = append(procedures, match)
		}
	}

	// Look for step-by-step instructions
	stepPattern := regexp.MustCompile(`(?i)(step\s+\d+|first|second|third|next|then|finally)[^.!?]*?\.`)
	stepMatches := stepPattern.FindAllString(content, -1)

	for _, match := range stepMatches {
		if len(match) > 10 {
			procedures = append(procedures, match)
		}
	}

	return procedures
}

// ConsolidateSimilarKnowledge finds and consolidates similar knowledge pieces
func (sed *Sedimenter) ConsolidateSimilarKnowledge(minConfidence float64) error {
	// Find all knowledge items with low-medium confidence
	items, err := sed.searchKnowledgeByConfidence(minConfidence)
	if err != nil {
		return err
	}

	// Group similar items and consolidate them
	consolidatedGroups := sed.groupSimilarItems(items)

	for _, group := range consolidatedGroups {
		if len(group) > 1 {
			// Create a consolidated knowledge item
			consolidatedKey := fmt.Sprintf("consolidated:%d", time.Now().UnixNano())

			// Combine the content from the group
			var combinedContent strings.Builder
			combinedContent.WriteString("Consolidated knowledge from multiple sources:\n")
			avgConfidence := 0.0

			for _, item := range group {
				combinedContent.WriteString(fmt.Sprintf("- %s\n", item.Content))
				avgConfidence += item.Confidence
			}

			avgConfidence /= float64(len(group))

			// Save the consolidated item
			core := &MemoryCore{db: sed.db}
			if err := core.Save(consolidatedKey, combinedContent.String()); err != nil {
				continue
			}

			// Set confidence based on average plus a boost for consolidation
			boostedConfidence := avgConfidence*0.8 + 0.2
			if boostedConfidence > 1.0 {
				boostedConfidence = 1.0
			}

			if err := sed.confidenceMgr.UpdateConfidence(consolidatedKey, boostedConfidence, "consolidation"); err != nil {
				continue
			}
		}
	}

	return nil
}

// searchKnowledgeByConfidence finds knowledge items within a confidence range
func (sed *Sedimenter) searchKnowledgeByConfidence(minConfidence float64) ([]SearchResult, error) {
	searcher := NewSearcher(sed.db, sed.confidenceMgr)
	return searcher.SearchByConfidenceRange(minConfidence, 1.0, 100)
}

// groupSimilarItems groups similar search results together
func (sed *Sedimenter) groupSimilarItems(items []SearchResult) [][]SearchResult {
	// This is a simplified grouping algorithm
	// In a real implementation, you'd use more sophisticated similarity measures

	var groups [][]SearchResult
	processed := make(map[int]bool)

	for i, item1 := range items {
		if processed[i] {
			continue
		}

		group := []SearchResult{item1}
		processed[i] = true

		for j, item2 := range items {
			if processed[j] || i == j {
				continue
			}

			if sed.isSimilarContent(item1.Content, item2.Content) {
				group = append(group, item2)
				processed[j] = true
			}
		}

		groups = append(groups, group)
	}

	return groups
}

// isSimilarContent determines if two content pieces are similar
func (sed *Sedimenter) isSimilarContent(content1, content2 string) bool {
	// Simplified similarity check
	c1Words := make(map[string]bool)
	for _, word := range strings.Fields(strings.ToLower(content1)) {
		if len(word) > 3 && !sed.isCommonWord(word) {
			c1Words[word] = true
		}
	}

	commonWords := 0
	totalWords := len(c1Words)

	for _, word := range strings.Fields(strings.ToLower(content2)) {
		word = strings.ToLower(strings.Trim(word, ".,!?;:\"'()"))
		if len(word) > 3 && !sed.isCommonWord(word) {
			if c1Words[word] {
				commonWords++
			}
			totalWords++
		}
	}

	if totalWords == 0 {
		return false
	}

	// If 30% or more words are common, consider them similar
	return float64(commonWords)/float64(totalWords) >= 0.3
}

// IdentifyLongTermKnowledge identifies information that should be kept long-term
func (sed *Sedimenter) IdentifyLongTermKnowledge() ([]SearchResult, error) {
	// Find high-confidence items that have been accessed multiple times
	querySQL := `
	SELECT key, content, confidence
	FROM memory_items
	WHERE confidence > 0.7 AND (
		julianday('now') - julianday(created_at) > 7  -- Exists for more than a week
		OR (
			julianday('now') - julianday(last_accessed) < 7  -- Accessed recently
			AND confidence > 0.6
		)
	)
	ORDER BY confidence DESC, last_accessed DESC
	LIMIT 50
	`

	rows, err := sed.db.Query(querySQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var key, content string
		var confidence float64

		if err := rows.Scan(&key, &content, &confidence); err != nil {
			return nil, err
		}

		results = append(results, SearchResult{
			Key:        key,
			Content:    content,
			Confidence: confidence,
			Score:      confidence,
		})
	}

	return results, nil
}

// ExtractSummary creates a summary of important information
func (sed *Sedimenter) ExtractSummary(keys []string) (string, error) {
	var summary strings.Builder
	summary.WriteString("Knowledge Summary:\n\n")

	for _, key := range keys {
		content, err := sed.getContentByKey(key)
		if err != nil {
			continue
		}

		confidence, err := sed.confidenceMgr.GetConfidence(key)
		if err != nil {
			confidence = 0.0
		}

		summary.WriteString(fmt.Sprintf("Item: %s (Confidence: %.2f)\n", key, confidence))
		summary.WriteString(fmt.Sprintf("Content: %s\n\n", content))
	}

	return summary.String(), nil
}

// getContentByKey retrieves content by key
func (sed *Sedimenter) getContentByKey(key string) (string, error) {
	var content string
	err := sed.db.QueryRow("SELECT content FROM memory_items WHERE key = ?", key).Scan(&content)
	return content, err
}
