// Package memory implements the Memory-Like-A-Tree architecture for PicoClaw
package memory

import (
	"fmt"
	"path/filepath"
	"time"
)

// MemoryLikeATree implements the complete Memory-Like-A-Tree architecture
type MemoryLikeATree struct {
	core        *MemoryCore
	confidence  *ConfidenceTracker
	indexer     *Indexer
	searcher    *Searcher
	sedimenter  *Sedimenter
	maintainer  *Maintainer
	initialized bool
}

// NewMemoryLikeATree creates a new Memory-Like-A-Tree instance
func NewMemoryLikeATree(workspace string) (*MemoryLikeATree, error) {
	// Initialize the core memory system
	core, err := NewMemoryCore(workspace)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize memory core: %v", err)
	}

	// Initialize confidence tracker
	confidence := NewConfidenceTracker(core.db)

	// Initialize indexer
	indexer := NewIndexer(core.db, confidence)

	// Initialize searcher
	searcher := NewSearcher(core.db, confidence)

	// Initialize sedimenter
	sedimenter := NewSedimenter(core.db, confidence, indexer)

	// Initialize maintainer
	archivePath := filepath.Join(workspace, "archive")
	maintainer := NewMaintainer(core.db, confidence, core, archivePath)

	mlat := &MemoryLikeATree{
		core:       core,
		confidence: confidence,
		indexer:    indexer,
		searcher:   searcher,
		sedimenter: sedimenter,
		maintainer: maintainer,
	}

	// Mark as initialized
	mlat.initialized = true

	return mlat, nil
}

// Save saves content to memory with indexing and confidence tracking
func (mlat *MemoryLikeATree) Save(key, content string) error {
	if !mlat.initialized {
		return fmt.Errorf("memory system not initialized")
	}

	// Save to core
	if err := mlat.core.Save(key, content); err != nil {
		return err
	}

	// Index the content
	if err := mlat.indexer.IndexContent(key, content); err != nil {
		return err
	}

	// Potentially increase confidence on save
	if err := mlat.confidence.UpdateConfidence(key, 0.9, "manual_save"); err != nil {
		return err
	}

	return nil
}

// Read reads content from memory and updates access metrics
func (mlat *MemoryLikeATree) Read(key string) (string, error) {
	if !mlat.initialized {
		return "", fmt.Errorf("memory system not initialized")
	}

	content, err := mlat.core.Read(key)
	if err != nil {
		return "", err
	}

	// Increase confidence when accessed
	if err := mlat.confidence.AdjustConfidenceByAccess(key); err != nil {
		// Don't fail the read if confidence adjustment fails
	}

	return content, nil
}

// Search performs a text-based search with confidence scoring
func (mlat *MemoryLikeATree) Search(query string, limit int) ([]SearchResult, error) {
	if !mlat.initialized {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mlat.searcher.Search(query, limit)
}

// FuzzySearch performs fuzzy search with configurable threshold
func (mlat *MemoryLikeATree) FuzzySearch(query string, threshold float64, limit int) ([]SearchResult, error) {
	if !mlat.initialized {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mlat.searcher.FuzzySearch(query, threshold, limit)
}

// SearchRelated finds related memory items
func (mlat *MemoryLikeATree) SearchRelated(key string, limit int) ([]SearchResult, error) {
	if !mlat.initialized {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mlat.searcher.SearchRelated(key, limit)
}

// SedimentKnowledge processes content to identify and preserve important knowledge
func (mlat *MemoryLikeATree) SedimentKnowledge(content, sourceKey string) error {
	if !mlat.initialized {
		return fmt.Errorf("memory system not initialized")
	}

	return mlat.sedimenter.SedimentKnowledge(content, sourceKey)
}

// GetConfidence returns the confidence score for a memory item
func (mlat *MemoryLikeATree) GetConfidence(key string) (float64, error) {
	if !mlat.initialized {
		return 0, fmt.Errorf("memory system not initialized")
	}

	return mlat.confidence.GetConfidence(key)
}

// UpdateConfidence manually updates the confidence score for a memory item
func (mlat *MemoryLikeATree) UpdateConfidence(key string, confidence float64, reason string) error {
	if !mlat.initialized {
		return fmt.Errorf("memory system not initialized")
	}

	return mlat.confidence.UpdateConfidence(key, confidence, reason)
}

// GetHighConfidenceItems returns items with confidence above threshold
func (mlat *MemoryLikeATree) GetHighConfidenceItems(minConfidence float64, limit int) ([]string, []float64, error) {
	if !mlat.initialized {
		return nil, nil, fmt.Errorf("memory system not initialized")
	}

	return mlat.confidence.GetHighConfidenceItems(minConfidence, limit)
}

// IdentifyLongTermKnowledge identifies information that should be kept long-term
func (mlat *MemoryLikeATree) IdentifyLongTermKnowledge() ([]SearchResult, error) {
	if !mlat.initialized {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mlat.sedimenter.IdentifyLongTermKnowledge()
}

// RunPeriodicMaintenance runs a full maintenance cycle
func (mlat *MemoryLikeATree) RunPeriodicMaintenance() error {
	if !mlat.initialized {
		return fmt.Errorf("memory system not initialized")
	}

	return mlat.maintainer.PeriodicMaintenance()
}

// ScheduleDailyTasks schedules daily maintenance tasks
func (mlat *MemoryLikeATree) ScheduleDailyTasks() {
	if !mlat.initialized {
		return
	}

	mlat.maintainer.ScheduleDailyTasks()
}

// ConsolidateSimilarKnowledge finds and consolidates similar knowledge pieces
func (mlat *MemoryLikeATree) ConsolidateSimilarKnowledge(minConfidence float64) error {
	if !mlat.initialized {
		return fmt.Errorf("memory system not initialized")
	}

	return mlat.sedimenter.ConsolidateSimilarKnowledge(minConfidence)
}

// ForceCleanup allows manual cleanup of low-confidence items
func (mlat *MemoryLikeATree) ForceCleanup(confidenceThreshold float64) error {
	if !mlat.initialized {
		return fmt.Errorf("memory system not initialized")
	}

	return mlat.maintainer.ForceCleanup(confidenceThreshold)
}

// GetAllKeys returns all memory item keys
func (mlat *MemoryLikeATree) GetAllKeys() ([]string, error) {
	if !mlat.initialized {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mlat.searcher.GetAllKeys()
}

// GetItemsByState returns items filtered by their lifecycle state
func (mlat *MemoryLikeATree) GetItemsByState(state ConfidenceState, limit int) ([]string, []float64, error) {
	if !mlat.initialized {
		return nil, nil, fmt.Errorf("memory system not initialized")
	}

	return mlat.confidence.GetItemsByState(state, limit)
}

// GetSproutItems returns items in the sprout state (newly created knowledge)
func (mlat *MemoryLikeATree) GetSproutItems(limit int) ([]string, []float64, error) {
	return mlat.GetItemsByState(Sprout, limit)
}

// GetGreenLeafItems returns items in the green leaf state (high confidence)
func (mlat *MemoryLikeATree) GetGreenLeafItems(limit int) ([]string, []float64, error) {
	return mlat.GetItemsByState(GreenLeaf, limit)
}

// GetYellowLeafItems returns items in the yellow leaf state (moderate confidence)
func (mlat *MemoryLikeATree) GetYellowLeafItems(limit int) ([]string, []float64, error) {
	return mlat.GetItemsByState(YellowLeaf, limit)
}

// GetDeadLeafItems returns items in the dead leaf state (low confidence)
func (mlat *MemoryLikeATree) GetDeadLeafItems(limit int) ([]string, []float64, error) {
	return mlat.GetItemsByState(DeadLeaf, limit)
}

// GetSoilItems returns items in the soil state (ready for archiving)
func (mlat *MemoryLikeATree) GetSoilItems(limit int) ([]string, []float64, error) {
	return mlat.GetItemsByState(Soil, limit)
}

// Count returns the total count of indexed items
func (mlat *MemoryLikeATree) Count() (int, error) {
	if !mlat.initialized {
		return 0, fmt.Errorf("memory system not initialized")
	}

	return mlat.searcher.Count()
}

// Close closes the memory system and all resources
func (mlat *MemoryLikeATree) Close() error {
	if !mlat.initialized {
		return fmt.Errorf("memory system not initialized")
	}

	return mlat.core.Close()
}

// CreateSnapshot creates a snapshot of the current memory state
func (mlat *MemoryLikeATree) CreateSnapshot() error {
	if !mlat.initialized {
		return fmt.Errorf("memory system not initialized")
	}

	// Perform database backup/vacuum
	return mlat.maintainer.VacuumDatabase()
}

// LoadFromSnapshot loads memory state from a snapshot
func (mlat *MemoryLikeATree) LoadFromSnapshot() error {
	if !mlat.initialized {
		return fmt.Errorf("memory system not initialized")
	}

	// In a full implementation, this would restore from backup
	// For now, just verify the DB is accessible
	var count int
	err := mlat.core.db.QueryRow("SELECT COUNT(*) FROM memory_items").Scan(&count)
	return err
}

// RunScheduledTasks runs all scheduled maintenance tasks
func (mlat *MemoryLikeATree) RunScheduledTasks() {
	if !mlat.initialized {
		return
	}

	mlat.maintainer.RunScheduledTasks()
}

// GetCurrentStats returns current memory statistics
func (mlat *MemoryLikeATree) GetCurrentStats() (map[string]interface{}, error) {
	if !mlat.initialized {
		return nil, fmt.Errorf("memory system not initialized")
	}

	stats := make(map[string]interface{})

	count, err := mlat.Count()
	if err != nil {
		return nil, err
	}
	stats["total_items"] = count

	// Count high confidence items
	highConfKeys, _, err := mlat.GetHighConfidenceItems(0.7, 1000)
	if err != nil {
		return nil, err
	}
	stats["high_confidence_items"] = len(highConfKeys)

	// Estimate database size
	var size int
	err = mlat.core.db.QueryRow("SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Scan(&size)
	if err != nil {
		// Fallback: set size to -1 to indicate unknown
		size = -1
	}
	stats["estimated_size_bytes"] = size

	stats["timestamp"] = time.Now().UTC()

	return stats, nil
}