// Package memory implements the confidence tracking system for Memory-Like-A-Tree architecture
package memory

import (
	"database/sql"
	"fmt"
	"strings"
)

// ConfidenceTracker manages confidence scores for memory items
type ConfidenceTracker struct {
	db *sql.DB
}

// NewConfidenceTracker creates a new confidence tracker
func NewConfidenceTracker(db *sql.DB) *ConfidenceTracker {
	return &ConfidenceTracker{
		db: db,
	}
}

// UpdateConfidence updates the confidence score for a memory item
func (ct *ConfidenceTracker) UpdateConfidence(key string, confidence float64, reason string) error {
	// Normalize confidence to 0-1 range
	if confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
	}

	// Update the confidence score
	query := "UPDATE memory_items SET confidence = ? WHERE key = ?"
	_, err := ct.db.Exec(query, confidence, key)
	return err
}

// AdjustConfidenceByAccess increases confidence when item is accessed
func (ct *ConfidenceTracker) AdjustConfidenceByAccess(key string) error {
	query := `
	SELECT confidence FROM memory_items WHERE key = ?
	`
	var currentConfidence float64
	err := ct.db.QueryRow(query, key).Scan(&currentConfidence)
	if err != nil {
		return err
	}

	// Increase confidence by a small amount when accessed
	newConfidence := currentConfidence + 0.05
	if newConfidence > 1.0 {
		newConfidence = 1.0
	}

	return ct.UpdateConfidence(key, newConfidence, "access")
}

// AdjustConfidenceByAssociation increases confidence for related items
func (ct *ConfidenceTracker) AdjustConfidenceByAssociation(key string, boostFactor float64) error {
	// Get current confidence
	query := "SELECT confidence FROM memory_items WHERE key = ?"
	var currentConfidence float64
	err := ct.db.QueryRow(query, key).Scan(&currentConfidence)
	if err != nil {
		return err
	}

	// Apply boost factor
	newConfidence := currentConfidence + (1-currentConfidence)*boostFactor
	if newConfidence > 1.0 {
		newConfidence = 1.0
	}

	return ct.UpdateConfidence(key, newConfidence, "association")
}

// GetConfidence gets the confidence score for a memory item
func (ct *ConfidenceTracker) GetConfidence(key string) (float64, error) {
	var confidence float64
	err := ct.db.QueryRow("SELECT confidence FROM memory_items WHERE key = ?", key).Scan(&confidence)
	return confidence, err
}

// GetConfidences gets confidence scores for multiple items
func (ct *ConfidenceTracker) GetConfidences(keys []string) (map[string]float64, error) {
	result := make(map[string]float64)

	// Create placeholders for the IN clause
	placeholders := strings.Repeat("?,", len(keys)-1) + "?"
	query := fmt.Sprintf("SELECT key, confidence FROM memory_items WHERE key IN (%s)", placeholders)

	stmt, err := ct.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	args := make([]interface{}, len(keys))
	for i, v := range keys {
		args[i] = v
	}

	rows, err := stmt.Query(args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var confidence float64
		if err := rows.Scan(&key, &confidence); err != nil {
			return nil, err
		}
		result[key] = confidence
	}

	return result, nil
}

// DecayConfidence decays confidence scores over time
func (ct *ConfidenceTracker) DecayConfidence(decayRate float64, maxAgeHours int) error {
	// Calculate decay multiplier based on age and decay rate
	decayMultiplier := 1.0 - decayRate

	query := `
	UPDATE memory_items
	SET confidence = confidence * ?
	WHERE last_accessed < datetime('now', '-? hours')
	AND confidence > 0.1  -- Don't decay very important items too much
	`

	_, err := ct.db.Exec(query, decayMultiplier, maxAgeHours)
	return err
}

// DecayAllConfidence applies gradual decay to all memories
func (ct *ConfidenceTracker) DecayAllConfidence(baseDecayRate float64) error {
	// Exponential decay based on time since last access
	query := `
	UPDATE memory_items
	SET confidence = CASE
		WHEN confidence > ? THEN confidence * (1.0 - (? / 2.0))  -- Less decay for high confidence items
		ELSE confidence * (1.0 - ?)  -- More decay for lower confidence items
	END
	WHERE confidence > 0.01  -- Keep minimum confidence
	`

	_, err := ct.db.Exec(query, 0.5, baseDecayRate, baseDecayRate)
	return err
}

// CleanLowConfidence removes low-confidence items
func (ct *ConfidenceTracker) CleanLowConfidence(threshold float64) error {
	query := "DELETE FROM memory_items WHERE confidence < ? AND last_accessed < datetime('now', '-24 hours')"
	_, err := ct.db.Exec(query, threshold)
	return err
}

// ConfidenceState represents the lifecycle state of a memory item
type ConfidenceState int

const (
	Unknown ConfidenceState = iota
	Sprout // 0.7-0.9 - Newly created knowledge
	GreenLeaf // 0.8-1.0 - Frequently used and highly confident
	YellowLeaf // 0.5-0.8 - Less frequently used
	DeadLeaf // 0.3-0.5 - Rarely used
	Soil // < 0.3 - Ready for archiving/purging
)

// GetConfidenceState returns the lifecycle state of a memory item based on confidence
func (ct *ConfidenceTracker) GetConfidenceState(confidence float64) ConfidenceState {
	switch {
	case confidence >= 0.8:
		return GreenLeaf
	case confidence >= 0.7:
		return Sprout
	case confidence >= 0.5:
		return YellowLeaf
	case confidence >= 0.3:
		return DeadLeaf
	default:
		return Soil
	}
}

// GetConfidenceRange returns the confidence range for a given state
func (ct *ConfidenceTracker) GetConfidenceRange(state ConfidenceState) (min, max float64) {
	switch state {
	case GreenLeaf:
		return 0.8, 1.0
	case Sprout:
		return 0.7, 0.8
	case YellowLeaf:
		return 0.5, 0.7
	case DeadLeaf:
		return 0.3, 0.5
	case Soil:
		return 0.0, 0.3
	default:
		return 0.0, 1.0
	}
}

// GetItemsByState returns items filtered by their lifecycle state
func (ct *ConfidenceTracker) GetItemsByState(state ConfidenceState, limit int) ([]string, []float64, error) {
	min, max := ct.GetConfidenceRange(state)

	query := `
	SELECT key, confidence
	FROM memory_items
	WHERE confidence >= ? AND confidence < ?
	ORDER BY confidence DESC
	LIMIT ?
	`

	rows, err := ct.db.Query(query, min, max, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var keys []string
	var confidences []float64

	for rows.Next() {
		var key string
		var confidence float64
		if err := rows.Scan(&key, &confidence); err != nil {
			return nil, nil, err
		}
		keys = append(keys, key)
		confidences = append(confidences, confidence)
	}

	return keys, confidences, nil
}

// GetHighConfidenceItems returns items with confidence above threshold
func (ct *ConfidenceTracker) GetHighConfidenceItems(minConfidence float64, limit int) ([]string, []float64, error) {
	query := `
	SELECT key, confidence
	FROM memory_items
	WHERE confidence >= ?
	ORDER BY confidence DESC
	LIMIT ?
	`

	rows, err := ct.db.Query(query, minConfidence, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var keys []string
	var confidences []float64

	for rows.Next() {
		var key string
		var confidence float64
		if err := rows.Scan(&key, &confidence); err != nil {
			return nil, nil, err
		}
		keys = append(keys, key)
		confidences = append(confidences, confidence)
	}

	return keys, confidences, nil
}