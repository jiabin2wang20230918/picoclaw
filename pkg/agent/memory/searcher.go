// Package memory implements the searcher for Memory-Like-A-Tree architecture
package memory

import (
	"database/sql"
	"strings"
	"time"
)

// Searcher provides search capabilities over indexed memory with confidence scoring
type Searcher struct {
	db             *sql.DB
	confidenceMgr  *ConfidenceTracker
}

// NewSearcher creates a new searcher with confidence tracking
func NewSearcher(db *sql.DB, confidenceMgr *ConfidenceTracker) *Searcher {
	return &Searcher{
		db:            db,
		confidenceMgr: confidenceMgr,
	}
}

type SearchResult = MemoryRecord

// Search performs a text-based search with confidence scoring
func (s *Searcher) Search(query string, limit int) ([]SearchResult, error) {
	// First, normalize the query
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil, nil
	}

	// Prepare query for LIKE search
	queryPattern := "%" + query + "%"

	// Perform search with confidence weighting
	querySQL := `
	SELECT key, content, confidence, memory_type, source_key, tags_json, reason, updated_at
	FROM memory_items
	WHERE LOWER(content) LIKE ?
	ORDER BY confidence DESC, last_accessed DESC
	LIMIT ?
	`

	rows, err := s.db.Query(querySQL, queryPattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var key, content string
		var confidence float64
		var memoryType, sourceKey, tagsJSON, reason string
		var updatedAtRaw string

		if err := rows.Scan(&key, &content, &confidence, &memoryType, &sourceKey, &tagsJSON, &reason, &updatedAtRaw); err != nil {
			return nil, err
		}

		// Calculate a relevance score based on how closely the query matches the content
		relevanceScore := s.calculateRelevance(query, content)

		// Combine relevance and confidence scores
		combinedScore := relevanceScore*0.3 + confidence*0.7 // Weight confidence more heavily

		results = append(results, SearchResult{
			Key:        key,
			Content:    content,
			Type:       normalizeMemoryType(MemoryType(memoryType)),
			SourceKey:  sourceKey,
			Confidence: confidence,
			Score:      combinedScore,
			Tags:       tagsFromJSON(tagsJSON),
			Reason:     reason,
			UpdatedAt:  parseSQLiteTime(updatedAtRaw),
		})
	}

	// Sort results by combined score
	s.sortResultsByScore(results)

	return results, nil
}

// calculateRelevance calculates a basic relevance score based on query match
func (s *Searcher) calculateRelevance(query, content string) float64 {
	queryWords := strings.Fields(strings.ToLower(query))
	contentLower := strings.ToLower(content)

	matches := 0
	totalWeight := 0.0

	for _, word := range queryWords {
		if strings.Contains(contentLower, word) {
			matches++
			// Give more weight to longer query terms
			totalWeight += float64(len(word))
		}
	}

	if len(queryWords) == 0 {
		return 0
	}

	// Normalize to 0-1 range
	relevance := float64(matches) / float64(len(queryWords))
	if relevance > 1.0 {
		relevance = 1.0
	}

	return relevance
}

// sortResultsByScore sorts results by combined score in descending order
func (s *Searcher) sortResultsByScore(results []SearchResult) {
	// Simple bubble sort for now - for better performance, use sort.Slice
	n := len(results)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if results[j].Score < results[j+1].Score {
				results[j], results[j+1] = results[j+1], results[j]
			}
		}
	}
}

// FuzzySearch performs fuzzy search with configurable threshold
func (s *Searcher) FuzzySearch(query string, threshold float64, limit int) ([]SearchResult, error) {
	// This is a simplified fuzzy search implementation
	// In a full implementation, you might use more sophisticated algorithms

	allResults, err := s.Search(query, 100) // Get more results for filtering
	if err != nil {
		return nil, err
	}

	// Filter results based on threshold
	var filteredResults []SearchResult
	for _, result := range allResults {
		if result.Score >= threshold {
			filteredResults = append(filteredResults, result)
			if len(filteredResults) >= limit {
				break
			}
		}
	}

	return filteredResults, nil
}

// SearchByConfidenceRange searches for items within a confidence range
func (s *Searcher) SearchByConfidenceRange(minConfidence, maxConfidence float64, limit int) ([]SearchResult, error) {
	querySQL := `
	SELECT key, content, confidence, memory_type, source_key, tags_json, reason, updated_at
	FROM memory_items
	WHERE confidence BETWEEN ? AND ?
	ORDER BY confidence DESC
	LIMIT ?
	`

	rows, err := s.db.Query(querySQL, minConfidence, maxConfidence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var key, content string
		var confidence float64
		var memoryType, sourceKey, tagsJSON, reason string
		var updatedAtRaw string

		if err := rows.Scan(&key, &content, &confidence, &memoryType, &sourceKey, &tagsJSON, &reason, &updatedAtRaw); err != nil {
			return nil, err
		}

		// Calculate a relevance score (in this case, it's just confidence)
		results = append(results, SearchResult{
			Key:        key,
			Content:    content,
			Type:       normalizeMemoryType(MemoryType(memoryType)),
			SourceKey:  sourceKey,
			Confidence: confidence,
			Score:      confidence,
			Tags:       tagsFromJSON(tagsJSON),
			Reason:     reason,
			UpdatedAt:  parseSQLiteTime(updatedAtRaw),
		})
	}

	return results, nil
}

// SearchRelated finds related memory items to a given key
func (s *Searcher) SearchRelated(key string, limit int) ([]SearchResult, error) {
	// Get the ID of the source item
	var sourceID int
	err := s.db.QueryRow("SELECT id FROM memory_items WHERE key = ?", key).Scan(&sourceID)
	if err != nil {
		return nil, err
	}

	// Find related items through relationships table
	querySQL := `
	SELECT mi.key, mi.content, mi.confidence, mi.memory_type, mi.source_key, mi.tags_json, mi.reason, mi.updated_at
	FROM memory_items mi
	JOIN memory_relations mr ON mi.id = mr.to_item_id
	WHERE mr.from_item_id = ?
	ORDER BY mr.confidence DESC
	LIMIT ?
	`

	rows, err := s.db.Query(querySQL, sourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var key, content string
		var confidence float64
		var memoryType, sourceKey, tagsJSON, reason string
		var updatedAtRaw string

		if err := rows.Scan(&key, &content, &confidence, &memoryType, &sourceKey, &tagsJSON, &reason, &updatedAtRaw); err != nil {
			return nil, err
		}

		results = append(results, SearchResult{
			Key:        key,
			Content:    content,
			Type:       normalizeMemoryType(MemoryType(memoryType)),
			SourceKey:  sourceKey,
			Confidence: confidence,
			Score:      confidence,
			Tags:       tagsFromJSON(tagsJSON),
			Reason:     reason,
			UpdatedAt:  parseSQLiteTime(updatedAtRaw),
		})
	}

	return results, nil
}

func parseSQLiteTime(raw string) time.Time {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// SearchReverseRelated finds items that relate TO the given key
func (s *Searcher) SearchReverseRelated(key string, limit int) ([]SearchResult, error) {
	// Get the ID of the target item
	var targetID int
	err := s.db.QueryRow("SELECT id FROM memory_items WHERE key = ?", key).Scan(&targetID)
	if err != nil {
		return nil, err
	}

	// Find items that have relationships to this item
	querySQL := `
	SELECT mi.key, mi.content, mi.confidence, mi.memory_type, mi.source_key, mi.tags_json, mi.reason, mi.updated_at
	FROM memory_items mi
	JOIN memory_relations mr ON mi.id = mr.from_item_id
	WHERE mr.to_item_id = ?
	ORDER BY mr.confidence DESC
	LIMIT ?
	`

	rows, err := s.db.Query(querySQL, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var key, content string
		var confidence float64
		var memoryType, sourceKey, tagsJSON, reason string
		var updatedAtRaw string

		if err := rows.Scan(&key, &content, &confidence, &memoryType, &sourceKey, &tagsJSON, &reason, &updatedAtRaw); err != nil {
			return nil, err
		}

		results = append(results, SearchResult{
			Key:        key,
			Content:    content,
			Type:       normalizeMemoryType(MemoryType(memoryType)),
			SourceKey:  sourceKey,
			Confidence: confidence,
			Score:      confidence,
			Tags:       tagsFromJSON(tagsJSON),
			Reason:     reason,
			UpdatedAt:  parseSQLiteTime(updatedAtRaw),
		})
	}

	return results, nil
}

// SearchRecent finds recently accessed items
func (s *Searcher) SearchRecent(hours int, limit int) ([]SearchResult, error) {
	querySQL := `
	SELECT key, content, confidence, memory_type, source_key, tags_json, reason, updated_at
	FROM memory_items
	WHERE last_accessed > datetime('now', '-? hours')
	ORDER BY last_accessed DESC
	LIMIT ?
	`

	rows, err := s.db.Query(querySQL, hours, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var key, content string
		var confidence float64
		var memoryType, sourceKey, tagsJSON, reason string
		var updatedAtRaw string

		if err := rows.Scan(&key, &content, &confidence, &memoryType, &sourceKey, &tagsJSON, &reason, &updatedAtRaw); err != nil {
			return nil, err
		}

		results = append(results, SearchResult{
			Key:        key,
			Content:    content,
			Type:       normalizeMemoryType(MemoryType(memoryType)),
			SourceKey:  sourceKey,
			Confidence: confidence,
			Score:      confidence, // Could weight recency higher
			Tags:       tagsFromJSON(tagsJSON),
			Reason:     reason,
			UpdatedAt:  parseSQLiteTime(updatedAtRaw),
		})
	}

	return results, nil
}

// GetAllKeys returns all memory item keys
func (s *Searcher) GetAllKeys() ([]string, error) {
	querySQL := "SELECT key FROM memory_items ORDER BY last_accessed DESC"
	rows, err := s.db.Query(querySQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}

	return keys, nil
}

// Count returns the total count of indexed items
func (s *Searcher) Count() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM memory_items").Scan(&count)
	return count, err
}
