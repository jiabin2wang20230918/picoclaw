// Package memory implements the indexer for Memory-Like-A-Tree architecture
package memory

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Indexer manages the indexing of memory content with confidence tracking
type Indexer struct {
	db             *sql.DB
	confidenceMgr  *ConfidenceTracker
}

// NewIndexer creates a new indexer with confidence tracking
func NewIndexer(db *sql.DB, confidenceMgr *ConfidenceTracker) *Indexer {
	return &Indexer{
		db:            db,
		confidenceMgr: confidenceMgr,
	}
}

// IndexContent indexes content with its key and assigns initial confidence
func (idx *Indexer) IndexContent(key, content string) error {
	// Save the content using the core memory system
	core := &MemoryCore{db: idx.db}
	if err := core.Save(key, content); err != nil {
		return err
	}

	// Create related memory entries based on content analysis
	return idx.createRelatedEntries(key, content)
}

// createRelatedEntries creates related entries based on content analysis
func (idx *Indexer) createRelatedEntries(parentKey, content string) error {
	// Extract entities and keywords from content
	entities := idx.extractEntities(content)
	keywords := idx.extractKeywords(content)

	// Create related entries for entities
	for _, entity := range entities {
		entityKey := fmt.Sprintf("entity:%s", entity)
		confidence := 0.7 // High confidence for named entities
		entityContent := fmt.Sprintf("Entity: %s (found in context of %s)", entity, parentKey)

		// Save entity
		core := &MemoryCore{db: idx.db}
		if err := core.Save(entityKey, entityContent); err != nil {
			continue // Continue even if one entity fails
		}

		// Update confidence
		if err := idx.confidenceMgr.UpdateConfidence(entityKey, confidence, "entity_extraction"); err != nil {
			continue
		}

		// Create relationship
		if err := idx.createRelationship(parentKey, entityKey, "mentions"); err != nil {
			continue
		}
	}

	// Create related entries for keywords
	for _, keyword := range keywords {
		keywordKey := fmt.Sprintf("keyword:%s", keyword)
		confidence := 0.5 // Medium confidence for keywords
		keywordContent := fmt.Sprintf("Keyword: %s (found in context of %s)", keyword, parentKey)

		// Save keyword
		core := &MemoryCore{db: idx.db}
		if err := core.Save(keywordKey, keywordContent); err != nil {
			continue // Continue even if one keyword fails
		}

		// Update confidence
		if err := idx.confidenceMgr.UpdateConfidence(keywordKey, confidence, "keyword_extraction"); err != nil {
			continue
		}

		// Create relationship
		if err := idx.createRelationship(parentKey, keywordKey, "contains_keyword"); err != nil {
			continue
		}
	}

	return nil
}

// extractEntities extracts named entities from content (simplified implementation)
func (idx *Indexer) extractEntities(content string) []string {
	// This is a simplified entity extraction
	// In a real implementation, you would use NLP libraries

	// Look for capitalized words that might be entities
	re := regexp.MustCompile(`\b[A-Z][a-z]{2,}\b`)
	matches := re.FindAllString(content, -1)

	// Remove duplicates and common words
	entities := make(map[string]bool)
	for _, match := range matches {
		// Filter out common words that are unlikely to be entities
		if !idx.isCommonWord(match) {
			entities[strings.ToLower(match)] = true
		}
	}

	var result []string
	for entity := range entities {
		result = append(result, entity)
	}

	return result
}

// extractKeywords extracts important keywords from content (simplified implementation)
func (idx *Indexer) extractKeywords(content string) []string {
	// Simple keyword extraction - in real implementation would use NLP
	words := strings.Fields(content)

	keywords := make(map[string]bool)
	for _, word := range words {
		word = strings.Trim(word, ".,!?;:\"'()")
		word = strings.ToLower(word)

		// Filter for meaningful words (length > 3, not common words)
		if len(word) > 3 && !idx.isCommonWord(word) {
			keywords[word] = true
		}
	}

	var result []string
	for keyword := range keywords {
		result = append(result, keyword)
	}

	return result
}

// isCommonWord checks if a word is common and probably not an important entity/keyword
func (idx *Indexer) isCommonWord(word string) bool {
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
	}

	return commonWords[strings.ToLower(word)]
}

// createRelationship creates a relationship between two memory items
func (idx *Indexer) createRelationship(fromKey, toKey, relationType string) error {
	// First get the IDs of the memory items
	var fromID, toID int
	err := idx.db.QueryRow("SELECT id FROM memory_items WHERE key = ?", fromKey).Scan(&fromID)
	if err != nil {
		return err
	}

	err = idx.db.QueryRow("SELECT id FROM memory_items WHERE key = ?", toKey).Scan(&toID)
	if err != nil {
		return err
	}

	// Insert the relationship
	query := `
	INSERT INTO memory_relations (from_item_id, to_item_id, relation_type, confidence)
	VALUES (?, ?, ?, 0.8)
	ON CONFLICT(from_item_id, to_item_id) DO UPDATE SET
		relation_type = excluded.relation_type,
		confidence = confidence * 1.1  -- Boost confidence if relationship is reinforced
	`

	_, err = idx.db.Exec(query, fromID, toID, relationType)
	return err
}

// IndexWithTimestamp indexes content with timestamp information
func (idx *Indexer) IndexWithTimestamp(key, content string, timestamp time.Time) error {
	// Add temporal information to content
	temporalContent := fmt.Sprintf("%s\n[Timestamp: %s]", content, timestamp.Format(time.RFC3339))

	return idx.IndexContent(key, temporalContent)
}

// BulkIndex indexes multiple content items at once
func (idx *Indexer) BulkIndex(items map[string]string) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	for key, content := range items {
		if err := idx.IndexContent(key, content); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// UpdateIndex updates an existing index entry
func (idx *Indexer) UpdateIndex(key, content string) error {
	return idx.IndexContent(key, content)
}

// RemoveIndex removes an index entry
func (idx *Indexer) RemoveIndex(key string) error {
	query := "DELETE FROM memory_items WHERE key = ?"
	_, err := idx.db.Exec(query, key)
	return err
}