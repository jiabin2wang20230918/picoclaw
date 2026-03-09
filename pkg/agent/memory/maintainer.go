// Package memory implements the maintainer for Memory-Like-A-Tree architecture
package memory

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Maintainer handles automatic maintenance tasks like decay, cleaning, and archiving
type Maintainer struct {
	db             *sql.DB
	confidenceMgr  *ConfidenceTracker
	core           *MemoryCore
	archivePath    string
}

// NewMaintainer creates a new maintenance handler
func NewMaintainer(db *sql.DB, confidenceMgr *ConfidenceTracker, core *MemoryCore, archivePath string) *Maintainer {
	return &Maintainer{
		db:            db,
		confidenceMgr: confidenceMgr,
		core:          core,
		archivePath:   archivePath,
	}
}

// DailyDecayer handles daily decay of confidence scores
type DailyDecayer struct {
	maintainer *Maintainer
}

// NewDailyDecayer creates a new decayer for daily tasks
func NewDailyDecayer(maintainer *Maintainer) *DailyDecayer {
	return &DailyDecayer{
		maintainer: maintainer,
	}
}

// RunDecay executes the daily decay process
func (dd *DailyDecayer) RunDecay() error {
	log.Printf("Starting daily confidence decay process...")

	// Apply gradual decay to all memories
	decayRate := 0.05 // 5% decay rate
	err := dd.maintainer.confidenceMgr.DecayAllConfidence(decayRate)
	if err != nil {
		return fmt.Errorf("failed to decay confidence: %v", err)
	}

	// Apply specific time-based decay for older memories
	err = dd.maintainer.confidenceMgr.DecayConfidence(0.1, 168) // 7 days
	if err != nil {
		return fmt.Errorf("failed to decay old memories: %v", err)
	}

	// Additional lifecycle-specific decay logic
	err = dd.applyLifecycleDecay()
	if err != nil {
		return fmt.Errorf("failed to apply lifecycle decay: %v", err)
	}

	log.Printf("Daily confidence decay process completed")
	return nil
}

// applyLifecycleDecay applies additional decay based on lifecycle states
func (dd *DailyDecayer) applyLifecycleDecay() error {
	// Process YellowLeaf items (0.5-0.8 confidence) with additional decay
	yellowLeafKeys, _, err := dd.maintainer.confidenceMgr.GetItemsByState(YellowLeaf, 1000)
	if err != nil {
		return fmt.Errorf("failed to get yellow leaf items: %v", err)
	}

	// Further reduce confidence for YellowLeaf items
	for _, key := range yellowLeafKeys {
		currentConfidence, err := dd.maintainer.confidenceMgr.GetConfidence(key)
		if err != nil {
			continue // Skip if can't get confidence
		}

		// Apply additional decay to yellow leaves
		newConfidence := currentConfidence * 0.95 // 5% additional decay
		if newConfidence < 0.3 {
			newConfidence = 0.3 // Minimum threshold for dead leaves
		}

		dd.maintainer.confidenceMgr.UpdateConfidence(key, newConfidence, "lifecycle_yellow_leaf_decay")
	}

	// Process DeadLeaf items (0.3-0.5 confidence) with higher decay risk
	deadLeafKeys, _, err := dd.maintainer.confidenceMgr.GetItemsByState(DeadLeaf, 1000)
	if err != nil {
		return fmt.Errorf("failed to get dead leaf items: %v", err)
	}

	// Higher chance of moving to soil for dead leaves
	for _, key := range deadLeafKeys {
		currentConfidence, err := dd.maintainer.confidenceMgr.GetConfidence(key)
		if err != nil {
			continue // Skip if can't get confidence
		}

		// Apply aggressive decay to dead leaves
		newConfidence := currentConfidence * 0.9 // 10% decay
		if newConfidence < 0.1 {
			newConfidence = 0.1 // Minimum threshold
		}

		dd.maintainer.confidenceMgr.UpdateConfidence(key, newConfidence, "lifecycle_dead_leaf_decay")
	}

	return nil
}

// DailyCleaner handles daily cleaning of low-value memories
type DailyCleaner struct {
	maintainer *Maintainer
}

// NewDailyCleaner creates a new cleaner for daily tasks
func NewDailyCleaner(maintainer *Maintainer) *DailyCleaner {
	return &DailyCleaner{
		maintainer: maintainer,
	}
}

// RunClean executes the daily cleaning process
func (dc *DailyCleaner) RunClean() error {
	log.Printf("Starting daily cleaning process...")

	// Move items to soil state first (confidence < 0.3)
	err := dc.moveToSoil()
	if err != nil {
		return fmt.Errorf("failed to move items to soil: %v", err)
	}

	// Remove memories in soil state that are older than 24 hours
	threshold := 0.3
	removed, err := dc.removeLowConfidenceItems(threshold)
	if err != nil {
		return fmt.Errorf("failed to remove low confidence items: %v", err)
	}

	log.Printf("Daily cleaning process completed. Removed %d low-confidence items", removed)

	// Archive low confidence items before complete removal
	err = dc.archiveBeforeRemoval()
	if err != nil {
		log.Printf("Warning: failed to archive before removal: %v", err)
	}

	return nil
}

// moveToSoil moves low confidence items to soil state for potential archiving
func (dc *DailyCleaner) moveToSoil() error {
	// Find items with confidence < 0.3 that aren't already archived
	query := `
	UPDATE memory_items
	SET confidence = confidence * 0.9  -- Reduce further to trigger archiving
	WHERE confidence < 0.3
	AND last_accessed < datetime('now', '-1 hour')
	`

	_, err := dc.maintainer.db.Exec(query)
	return err
}

// archiveBeforeRemoval archives items before they're completely removed
func (dc *DailyCleaner) archiveBeforeRemoval() error {
	// Get items that are close to being removed
	query := `
	SELECT key, content, confidence
	FROM memory_items
	WHERE confidence < 0.3
	AND last_accessed < datetime('now', '-24 hours')
	ORDER BY confidence DESC
	LIMIT 20
	`

	rows, err := dc.maintainer.db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	var itemsToArchive []struct {
		key        string
		content    string
		confidence float64
	}

	for rows.Next() {
		var item struct {
			key        string
			content    string
			confidence float64
		}
		err := rows.Scan(&item.key, &item.content, &item.confidence)
		if err != nil {
			continue
		}
		itemsToArchive = append(itemsToArchive, item)
	}

	if len(itemsToArchive) > 0 {
		// Create soil archive file
		soilDir := filepath.Join(dc.maintainer.archivePath, "soil")
		err := os.MkdirAll(soilDir, 0755)
		if err != nil {
			return fmt.Errorf("failed to create soil archive directory: %v", err)
		}

		soilFile := filepath.Join(soilDir, fmt.Sprintf("soil_%s.md", time.Now().Format("2006-01-02_15-04-05")))

		var content strings.Builder
		content.WriteString(fmt.Sprintf("# Soil Archive - %s\n\n", time.Now().Format("January 2, 2006 15:04:05")))
		content.WriteString("These items had low confidence and were candidates for removal, but preserved as they might be referenced by new knowledge.\n\n")

		for _, item := range itemsToArchive {
			content.WriteString(fmt.Sprintf("## %s (Confidence: %.2f)\n", item.key, item.confidence))
			content.WriteString(item.content)
			content.WriteString("\n\n")
		}

		err = os.WriteFile(soilFile, []byte(content.String()), 0644)
		if err != nil {
			return fmt.Errorf("failed to write soil archive: %v", err)
		}

		log.Printf("Created soil archive with %d items: %s", len(itemsToArchive), soilFile)
	}

	return nil
}

// removeLowConfidenceItems removes items with confidence below threshold
func (dc *DailyCleaner) removeLowConfidenceItems(threshold float64) (int, error) {
	// First, get the items that would be removed (for logging)
	query := "SELECT key, content FROM memory_items WHERE confidence < ? AND last_accessed < datetime('now', '-24 hours')"
	rows, err := dc.maintainer.db.Query(query, threshold)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key, content string
		if err := rows.Scan(&key, &content); err != nil {
			continue
		}
		keys = append(keys, key)
	}

	// Now remove the items
	err = dc.maintainer.confidenceMgr.CleanLowConfidence(threshold)
	if err != nil {
		return 0, err
	}

	return len(keys), nil
}

// ArchiveManager handles archiving and extraction of highlights
type ArchiveManager struct {
	maintainer  *Maintainer
	archivePath string
}

// NewArchiveManager creates a new archive manager
func NewArchiveManager(maintainer *Maintainer, archivePath string) *ArchiveManager {
	return &ArchiveManager{
		maintainer:  maintainer,
		archivePath: archivePath,
	}
}

// RunArchive executes the archiving process
func (am *ArchiveManager) RunArchive() error {
	log.Printf("Starting archiving process...")

	// Create archive directory if it doesn't exist
	err := os.MkdirAll(am.archivePath, 0755)
	if err != nil {
		return fmt.Errorf("failed to create archive directory: %v", err)
	}

	// Extract highlights from high-confidence memories
	highConfItems, _, err := am.maintainer.confidenceMgr.GetHighConfidenceItems(0.7, 50)
	if err != nil {
		return fmt.Errorf("failed to get high confidence items: %v", err)
	}

	if len(highConfItems) > 0 {
		// Create monthly archive
		monthDir := time.Now().Format("2006-01")
		monthPath := filepath.Join(am.archivePath, monthDir)
		err = os.MkdirAll(monthPath, 0755)
		if err != nil {
			return fmt.Errorf("failed to create monthly archive directory: %v", err)
		}

		// Generate highlights file
		highlightsFile := filepath.Join(monthPath, fmt.Sprintf("highlights_%s.md", time.Now().Format("2006-01-02")))

		var content strings.Builder
		content.WriteString(fmt.Sprintf("# Memory Highlights - %s\n\n", time.Now().Format("January 2, 2006")))

		for _, key := range highConfItems {
			itemContent, err := am.maintainer.core.Read(key)
			if err != nil {
				continue
			}

			confidence, err := am.maintainer.confidenceMgr.GetConfidence(key)
			if err != nil {
				continue
			}

			content.WriteString(fmt.Sprintf("## %s (Confidence: %.2f)\n", key, confidence))
			content.WriteString(itemContent)
			content.WriteString("\n\n")
		}

		// Write highlights to file
		err = os.WriteFile(highlightsFile, []byte(content.String()), 0644)
		if err != nil {
			return fmt.Errorf("failed to write highlights file: %v", err)
		}

		log.Printf("Archived %d high-confidence items to %s", len(highConfItems), highlightsFile)
	}

	log.Printf("Archiving process completed")
	return nil
}

// PeriodicMaintenance runs a full maintenance cycle
func (m *Maintainer) PeriodicMaintenance() error {
	log.Printf("Starting periodic maintenance...")

	// Create decayer and run decay
	decayer := NewDailyDecayer(m)
	err := decayer.RunDecay()
	if err != nil {
		log.Printf("Warning: Decayer failed: %v", err)
		// Continue with other maintenance tasks
	}

	// Create cleaner and run cleaning
	cleaner := NewDailyCleaner(m)
	err = cleaner.RunClean()
	if err != nil {
		log.Printf("Warning: Cleaner failed: %v", err)
		// Continue with other maintenance tasks
	}

	// Create archive manager and run archiving
	archiver := NewArchiveManager(m, m.archivePath)
	err = archiver.RunArchive()
	if err != nil {
		log.Printf("Warning: Archiver failed: %v", err)
		// This is non-critical, continue
	}

	log.Printf("Periodic maintenance completed")
	return nil
}

// ForceCleanup allows manual cleanup of low-confidence items
func (m *Maintainer) ForceCleanup(confidenceThreshold float64) error {
	log.Printf("Starting forced cleanup with threshold %.2f...", confidenceThreshold)

	// Remove low confidence items
	cleaner := NewDailyCleaner(m)
	removed, err := cleaner.removeLowConfidenceItems(confidenceThreshold)
	if err != nil {
		return fmt.Errorf("failed to force cleanup: %v", err)
	}

	log.Printf("Force cleanup completed. Removed %d items", removed)
	return nil
}

// PurgeOldItems removes items older than specified days
func (m *Maintainer) PurgeOldItems(days int) error {
	log.Printf("Starting purge of items older than %d days...", days)

	query := `
	DELETE FROM memory_items
	WHERE created_at < datetime('now', '-? days')
	AND confidence < 0.3  -- Only remove low-confidence old items
	`

	result, err := m.db.Exec(query, days)
	if err != nil {
		return fmt.Errorf("failed to purge old items: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %v", err)
	}

	log.Printf("Purge completed. Removed %d old items", rowsAffected)
	return nil
}

// VacuumDatabase optimizes the database
func (m *Maintainer) VacuumDatabase() error {
	log.Printf("Starting database vacuum...")

	_, err := m.db.Exec("VACUUM")
	if err != nil {
		return fmt.Errorf("failed to vacuum database: %v", err)
	}

	log.Printf("Database vacuum completed")
	return nil
}

// RunScheduledTasks runs all scheduled maintenance tasks
func (m *Maintainer) RunScheduledTasks() {
	// Run these tasks asynchronously
	go func() {
		log.Printf("Running scheduled maintenance tasks at %s", time.Now().Format(time.RFC3339))

		err := m.PeriodicMaintenance()
		if err != nil {
			log.Printf("Error in scheduled tasks: %v", err)
		}

		// Optionally run database vacuum weekly
		if time.Now().Weekday() == time.Sunday {
			err := m.VacuumDatabase()
			if err != nil {
				log.Printf("Database vacuum failed: %v", err)
			}
		}
	}()
}

// ScheduleDailyTasks schedules daily maintenance tasks
func (m *Maintainer) ScheduleDailyTasks() {
	go func() {
		for {
			// Calculate time until next 2 AM (for decayer)
			now := time.Now()
			nextRun := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
			if !nextRun.After(now) {
				nextRun = nextRun.Add(24 * time.Hour) // Next day at 2 AM
			}

			duration := nextRun.Sub(now)
			time.Sleep(duration)

			// Run decayer
			decayer := NewDailyDecayer(m)
			if err := decayer.RunDecay(); err != nil {
				log.Printf("Daily decayer failed: %v", err)
			}

			// Calculate time until next 3 AM (for cleaner)
			nextClean := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
			if !nextClean.After(now) {
				nextClean = nextClean.Add(24 * time.Hour) // Next day at 3 AM
			}

			duration = nextClean.Sub(now)
			time.Sleep(duration)

			// Run cleaner
			cleaner := NewDailyCleaner(m)
			if err := cleaner.RunClean(); err != nil {
				log.Printf("Daily cleaner failed: %v", err)
			}

			// Run archiver around 4 AM
			archiver := NewArchiveManager(m, m.archivePath)
			if err := archiver.RunArchive(); err != nil {
				log.Printf("Daily archiver failed: %v", err)
			}
		}
	}()
}