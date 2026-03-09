// Package memory provides the main memory manager for PicoClaw integrating the Memory-Like-A-Tree system
package memory

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
	"os"
)

// MemoryManager provides a high-level interface for memory operations
type MemoryManager struct {
	tree *MemoryLikeATree
	workspace string
}

// NewMemoryManager creates a new memory manager with the Memory-Like-A-Tree system
func NewMemoryManager(workspace string) (*MemoryManager, error) {
	tree, err := NewMemoryLikeATree(workspace)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Memory-Like-A-Tree system: %v", err)
	}

	manager := &MemoryManager{
		tree:      tree,
		workspace: workspace,
	}

	// Perform migration from legacy file-based storage if needed
	err = manager.migrateFromLegacyStorage()
	if err != nil {
		log.Printf("Warning: Failed to migrate legacy storage: %v", err)
	}

	// Start daily scheduled tasks
	manager.StartScheduledTasks()

	return manager, nil
}

// migrateFromLegacyStorage migrates data from traditional file-based storage to the new system
func (mm *MemoryManager) migrateFromLegacyStorage() error {
	memoryDir := filepath.Join(mm.workspace, "memory")
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	// Migrate long-term memory if it exists
	if _, err := os.Stat(memoryFile); err == nil {
		content, err := os.ReadFile(memoryFile)
		if err == nil {
			existingContent, readErr := mm.Read("MEMORY.md")
			if readErr != nil || existingContent == "" {
				// Only migrate if not already present in new system
				err = mm.Save("MEMORY.md", string(content))
				if err != nil {
					return fmt.Errorf("failed to migrate MEMORY.md: %v", err)
				}

				// Set appropriate confidence for migrated long-term memory
				err = mm.UpdateConfidence("MEMORY.md", 0.85, "migrated_long_term")
				if err != nil {
					log.Printf("Warning: Failed to set confidence for migrated MEMORY.md: %v", err)
				}

				log.Printf("Migrated long-term memory from legacy storage")
			}
		}
	}

	// Migrate daily notes from last 30 days
	for i := 0; i < 30; i++ {
		date := time.Now().AddDate(0, 0, -i)
		dateYYYYMMDD := date.Format("20060102") // Legacy format: YYYYMMDD
		dateYYYYMM := date.Format("200601")     // Legacy format: YYYYMM

		// The legacy file path: memory/YYYYMM/YYYYMMDD.md
		legacyFilePath := filepath.Join(memoryDir, dateYYYYMM, dateYYYYMMDD+".md")

		// New system key format: memory/YYYY-MM-DD.md
		newKey := fmt.Sprintf("memory/%s.md", date.Format("2006-01-02"))

		if _, err := os.Stat(legacyFilePath); err == nil {
			content, err := os.ReadFile(legacyFilePath)
			if err == nil {
				// Check if already exists in new system
				existingContent, readErr := mm.Read(newKey)
				if readErr != nil || existingContent == "" {
					err = mm.Save(newKey, string(content))
					if err != nil {
						log.Printf("Warning: Failed to migrate daily note %s: %v", newKey, err)
						continue
					}

					// Set confidence based on recency
					confidence := 0.7 - float64(i)*0.01
					if confidence < 0.4 {
						confidence = 0.4
					}
					err = mm.UpdateConfidence(newKey, confidence, "migrated_daily_note")
					if err != nil {
						log.Printf("Warning: Failed to set confidence for %s: %v", newKey, err)
					}

					log.Printf("Migrated daily note %s from legacy storage", dateYYYYMMDD)
				}
			}
		}
	}

	return nil
}

// StartScheduledTasks starts the scheduled maintenance tasks
func (mm *MemoryManager) StartScheduledTasks() {
	if mm.tree != nil {
		mm.tree.ScheduleDailyTasks()
	}
}

// Save stores content in the memory system with automatic indexing and confidence tracking
func (mm *MemoryManager) Save(key, content string) error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	return mm.tree.Save(key, content)
}

// Read retrieves content from memory and updates access metrics
func (mm *MemoryManager) Read(key string) (string, error) {
	if mm.tree == nil {
		return "", fmt.Errorf("memory system not initialized")
	}

	return mm.tree.Read(key)
}

// Search performs a text-based search with confidence scoring
func (mm *MemoryManager) Search(query string, limit int) ([]SearchResult, error) {
	if mm.tree == nil {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.Search(query, limit)
}

// FuzzySearch performs fuzzy search with configurable threshold
func (mm *MemoryManager) FuzzySearch(query string, threshold float64, limit int) ([]SearchResult, error) {
	if mm.tree == nil {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.FuzzySearch(query, threshold, limit)
}

// SearchRelated finds related memory items
func (mm *MemoryManager) SearchRelated(key string, limit int) ([]SearchResult, error) {
	if mm.tree == nil {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.SearchRelated(key, limit)
}

// SedimentKnowledge processes content to identify and preserve important knowledge
func (mm *MemoryManager) SedimentKnowledge(content, sourceKey string) error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	return mm.tree.SedimentKnowledge(content, sourceKey)
}

// GetConfidence returns the confidence score for a memory item
func (mm *MemoryManager) GetConfidence(key string) (float64, error) {
	if mm.tree == nil {
		return 0, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.GetConfidence(key)
}

// UpdateConfidence manually updates the confidence score for a memory item
func (mm *MemoryManager) UpdateConfidence(key string, confidence float64, reason string) error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	return mm.tree.UpdateConfidence(key, confidence, reason)
}

// GetHighConfidenceItems returns items with confidence above threshold
func (mm *MemoryManager) GetHighConfidenceItems(minConfidence float64, limit int) ([]string, []float64, error) {
	if mm.tree == nil {
		return nil, nil, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.GetHighConfidenceItems(minConfidence, limit)
}

// IdentifyLongTermKnowledge identifies information that should be kept long-term
func (mm *MemoryManager) IdentifyLongTermKnowledge() ([]SearchResult, error) {
	if mm.tree == nil {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.IdentifyLongTermKnowledge()
}

// RunPeriodicMaintenance runs a full maintenance cycle
func (mm *MemoryManager) RunPeriodicMaintenance() error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	return mm.tree.RunPeriodicMaintenance()
}

// ConsolidateSimilarKnowledge finds and consolidates similar knowledge pieces
func (mm *MemoryManager) ConsolidateSimilarKnowledge(minConfidence float64) error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	return mm.tree.ConsolidateSimilarKnowledge(minConfidence)
}

// ForceCleanup allows manual cleanup of low-confidence items
func (mm *MemoryManager) ForceCleanup(confidenceThreshold float64) error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	return mm.tree.ForceCleanup(confidenceThreshold)
}

// GetAllKeys returns all memory item keys
func (mm *MemoryManager) GetAllKeys() ([]string, error) {
	if mm.tree == nil {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.GetAllKeys()
}

// Count returns the total count of indexed items
func (mm *MemoryManager) Count() (int, error) {
	if mm.tree == nil {
		return 0, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.Count()
}

// GetCurrentStats returns current memory statistics
func (mm *MemoryManager) GetCurrentStats() (map[string]interface{}, error) {
	if mm.tree == nil {
		return nil, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.GetCurrentStats()
}

// Close closes the memory system and all resources
func (mm *MemoryManager) Close() error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	return mm.tree.Close()
}

// CreateSnapshot creates a snapshot of the current memory state
func (mm *MemoryManager) CreateSnapshot() error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	return mm.tree.CreateSnapshot()
}

// ReadLongTerm reads the traditional long-term memory (backward compatibility)
func (mm *MemoryManager) ReadLongTerm() string {
	if mm.tree == nil {
		return ""
	}

	content, err := mm.Read("MEMORY.md")
	if err != nil {
		return ""
	}

	return content
}

// WriteLongTerm writes to the traditional long-term memory (backward compatibility)
func (mm *MemoryManager) WriteLongTerm(content string) error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	return mm.Save("MEMORY.md", content)
}

// ReadToday reads today's daily note (backward compatibility)
func (mm *MemoryManager) ReadToday() string {
	if mm.tree == nil {
		return ""
	}

	todayKey := fmt.Sprintf("memory/%s.md", time.Now().Format("2006-01-02"))
	content, err := mm.Read(todayKey)
	if err != nil {
		return ""
	}

	return content
}

// AppendToday appends to today's daily note (backward compatibility)
func (mm *MemoryManager) AppendToday(content string) error {
	if mm.tree == nil {
		return fmt.Errorf("memory system not initialized")
	}

	todayKey := fmt.Sprintf("memory/%s.md", time.Now().Format("2006-01-02"))

	// Read existing content
	existingContent, err := mm.Read(todayKey)
	if err != nil {
		// If the file doesn't exist, create it with a header
		existingContent = fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
	}

	// Append new content
	updatedContent := existingContent + "\n" + content

	return mm.Save(todayKey, updatedContent)
}

// GetRecentDailyNotes returns daily notes from the last N days (backward compatibility)
func (mm *MemoryManager) GetRecentDailyNotes(days int) string {
	if mm.tree == nil {
		return ""
	}

	var result strings.Builder
	first := true

	for i := 0; i < days; i++ {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("2006-01-02")
		key := fmt.Sprintf("memory/%s.md", dateStr)

		content, err := mm.Read(key)
		if err != nil {
			continue // Skip missing days
		}

		if !first {
			result.WriteString("\n\n---\n\n")
		}

		result.WriteString(content)
		first = false
	}

	return result.String()
}

// GetItemsByState returns items filtered by their lifecycle state
func (mm *MemoryManager) GetItemsByState(state ConfidenceState, limit int) ([]string, []float64, error) {
	if mm.tree == nil {
		return nil, nil, fmt.Errorf("memory system not initialized")
	}

	return mm.tree.GetItemsByState(state, limit)
}

// GetSproutItems returns items in the sprout state (newly created knowledge)
func (mm *MemoryManager) GetSproutItems(limit int) ([]string, []float64, error) {
	return mm.GetItemsByState(Sprout, limit)
}

// GetGreenLeafItems returns items in the green leaf state (high confidence)
func (mm *MemoryManager) GetGreenLeafItems(limit int) ([]string, []float64, error) {
	return mm.GetItemsByState(GreenLeaf, limit)
}

// GetYellowLeafItems returns items in the yellow leaf state (moderate confidence)
func (mm *MemoryManager) GetYellowLeafItems(limit int) ([]string, []float64, error) {
	return mm.GetItemsByState(YellowLeaf, limit)
}

// GetDeadLeafItems returns items in the dead leaf state (low confidence)
func (mm *MemoryManager) GetDeadLeafItems(limit int) ([]string, []float64, error) {
	return mm.GetItemsByState(DeadLeaf, limit)
}

// GetSoilItems returns items in the soil state (ready for archiving)
func (mm *MemoryManager) GetSoilItems(limit int) ([]string, []float64, error) {
	return mm.GetItemsByState(Soil, limit)
}

// GetMemoryContext returns formatted memory context for the agent prompt (backward compatibility)
func (mm *MemoryManager) GetMemoryContext() string {
	if mm.tree == nil {
		return ""
	}

	var result strings.Builder

	longTerm := mm.ReadLongTerm()
	recentNotes := mm.GetRecentDailyNotes(3)

	if longTerm == "" && recentNotes == "" {
		return ""
	}

	if longTerm != "" {
		result.WriteString("## Long-term Memory\n\n")
		result.WriteString(longTerm)
	}

	if recentNotes != "" {
		if longTerm != "" {
			result.WriteString("\n\n---\n\n")
		}
		result.WriteString("## Recent Daily Notes\n\n")
		result.WriteString(recentNotes)
	}

	return result.String()
}