// Package memory implements the Memory-Like-A-Tree architecture for PicoClaw
// with indexer, search, sediment, confidence tracking, and maintenance mechanisms
package memory

import (
	"database/sql"
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
			last_accessed DATETIME DEFAULT CURRENT_TIMESTAMP
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

	return nil
}

// Save saves content to memory and creates/updates index entry
func (mc *MemoryCore) Save(key, content string) error {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	// Insert or update memory item with initial confidence
	query := `
	INSERT INTO memory_items (key, content, confidence, last_accessed)
	VALUES (?, ?, 1.0, datetime('now'))
	ON CONFLICT(key) DO UPDATE SET
		content = excluded.content,
		updated_at = excluded.updated_at,
		last_accessed = excluded.last_accessed`

	_, err := mc.db.Exec(query, key, content)
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