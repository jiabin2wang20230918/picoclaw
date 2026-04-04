// Package memory implements the Memory-Like-A-Tree architecture for PicoClaw
// with indexer, search, sediment, confidence tracking, and maintenance mechanisms
package memory

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// MemoryCore manages the workspace as the single source of truth
type MemoryCore struct {
	workspace   string
	memoryFile  string
	confidenceDB string
	db          *sql.DB
	mutex       sync.RWMutex
}

// NewMemoryCore creates a new memory core with workspace as single source of truth
func NewMemoryCore(workspace string) (*MemoryCore, error) {
	mc := &MemoryCore{
		workspace:   workspace,
		memoryFile:  filepath.Join(workspace, "MEMORY.md"),
		confidenceDB: filepath.Join(workspace, "memory.db"),
	}

	// Initialize confidence database
	if err := mc.initConfidenceDB(); err != nil {
		return nil, err
	}

	return mc, nil
}

// initConfidenceDB initializes the SQLite database for confidence tracking
func (mc *MemoryCore) initConfidenceDB() error {
	db, err := sql.Open("sqlite3", mc.confidenceDB)
	if err != nil {
		return err
	}

	mc.db = db

	// Create tables for confidence tracking
	queries := []string{
		`CREATE TABLE IF NOT EXISTS memory_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE NOT NULL,
			content TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			confidence REAL DEFAULT 1.0,
			last_accessed DATETIME DEFAULT CURRENT_TIMESTAMP,
			memory_type TEXT DEFAULT 'artifact',
			source_key TEXT DEFAULT '',
			tags_json TEXT DEFAULT '[]',
			reason TEXT DEFAULT ''
		)`,

		`CREATE TABLE IF NOT EXISTS memory_relations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			from_item_id INTEGER,
			to_item_id INTEGER,
			relation_type TEXT,
			confidence REAL DEFAULT 1.0,
			FOREIGN KEY(from_item_id) REFERENCES memory_items(id),
			FOREIGN KEY(to_item_id) REFERENCES memory_items(id)
		)`,

		`CREATE INDEX IF NOT EXISTS idx_memory_items_key ON memory_items(key)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_items_confidence ON memory_items(confidence)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_items_last_accessed ON memory_items(last_accessed)`,
	}

	for _, query := range queries {
		if _, err := mc.db.Exec(query); err != nil {
			return err
		}
	}

	for _, columnDef := range []string{
		"memory_type TEXT DEFAULT 'artifact'",
		"source_key TEXT DEFAULT ''",
		"tags_json TEXT DEFAULT '[]'",
		"reason TEXT DEFAULT ''",
	} {
		if err := mc.ensureColumn("memory_items", columnDef); err != nil {
			return err
		}
	}

	if _, err := mc.db.Exec(`CREATE INDEX IF NOT EXISTS idx_memory_items_type ON memory_items(memory_type)`); err != nil {
		return err
	}
	if _, err := mc.db.Exec(`CREATE INDEX IF NOT EXISTS idx_memory_items_source_key ON memory_items(source_key)`); err != nil {
		return err
	}

	return nil
}

func (mc *MemoryCore) ensureColumn(tableName string, columnDef string) error {
	columnName := columnDef
	if idx := len(columnDef); idx > 0 {
		for i, r := range columnDef {
			if r == ' ' || r == '\t' {
				columnName = columnDef[:i]
				break
			}
		}
	}

	rows, err := mc.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == columnName {
			return nil
		}
	}

	_, err = mc.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", tableName, columnDef))
	return err
}

// Save saves content to memory and creates/updates index entry
func (mc *MemoryCore) Save(key, content string) error {
	record := MemoryRecord{
		Key:       key,
		Content:   content,
		Type:      MemoryTypeArtifact,
		SourceKey: key,
	}

	var existingType, existingSourceKey, existingTagsJSON, existingReason string
	err := mc.db.QueryRow(
		"SELECT memory_type, source_key, tags_json, reason FROM memory_items WHERE key = ?",
		key,
	).Scan(&existingType, &existingSourceKey, &existingTagsJSON, &existingReason)
	if err == nil {
		record.Type = normalizeMemoryType(MemoryType(existingType))
		record.SourceKey = existingSourceKey
		record.Tags = tagsFromJSON(existingTagsJSON)
		record.Reason = existingReason
	}

	return mc.SaveRecord(MemoryRecord{
		Key:       record.Key,
		Content:   record.Content,
		Type:      record.Type,
		SourceKey: record.SourceKey,
		Tags:      record.Tags,
		Reason:    record.Reason,
	})
}

// SaveRecord stores content with structured metadata.
func (mc *MemoryCore) SaveRecord(record MemoryRecord) error {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	record.Type = normalizeMemoryType(record.Type)

	// Insert or update memory item with initial confidence
	query := `
	INSERT INTO memory_items (key, content, confidence, last_accessed, memory_type, source_key, tags_json, reason)
	VALUES (?, ?, 1.0, datetime('now'), ?, ?, ?, ?)
	ON CONFLICT(key) DO UPDATE SET
		content = excluded.content,
		updated_at = excluded.updated_at,
		last_accessed = excluded.last_accessed,
		memory_type = excluded.memory_type,
		source_key = excluded.source_key,
		tags_json = excluded.tags_json,
		reason = excluded.reason`

	sourceKey := record.SourceKey
	if sourceKey == "" {
		sourceKey = record.Key
	}
	_, err := mc.db.Exec(
		query,
		record.Key,
		record.Content,
		string(record.Type),
		sourceKey,
		tagsToJSON(record.Tags),
		record.Reason,
	)
	return err
}

// Read reads content from memory
func (mc *MemoryCore) Read(key string) (string, error) {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	var content string
	err := mc.db.QueryRow("SELECT content FROM memory_items WHERE key = ?", key).Scan(&content)
	if err != nil {
		return "", err
	}

	// Update last accessed time
	mc.db.Exec("UPDATE memory_items SET last_accessed = datetime('now') WHERE key = ?", key)

	return content, nil
}

// Close closes the database connection
func (mc *MemoryCore) Close() error {
	if mc.db != nil {
		return mc.db.Close()
	}
	return nil
}
