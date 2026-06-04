package memory

import (
	"os"
	"testing"
)

func TestMemoryManager_SaveRecordAndSearchStructuredMetadata(t *testing.T) {
	workspace, err := os.MkdirTemp("", "quantclaw-memory-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(workspace)

	manager, err := NewMemoryManager(workspace)
	if err != nil {
		t.Fatalf("NewMemoryManager: %v", err)
	}
	defer manager.Close()

	record := MemoryRecord{
		Key:       "pref:editor",
		Content:   "User prefers modal editing in terminal workflows.",
		Type:      MemoryTypePreference,
		SourceKey: "session:test",
		Tags:      []string{"editor", "workflow"},
		Reason:    "test_seed",
	}
	if err := manager.SaveRecord(record); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}

	results, err := manager.Search("prefers modal editing", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}

	got := results[0]
	if got.Type != MemoryTypePreference {
		t.Fatalf("type = %q, want %q", got.Type, MemoryTypePreference)
	}
	if got.SourceKey != "session:test" {
		t.Fatalf("source_key = %q, want %q", got.SourceKey, "session:test")
	}
	if got.Reason != "test_seed" {
		t.Fatalf("reason = %q, want %q", got.Reason, "test_seed")
	}
	if len(got.Tags) != 2 || got.Tags[0] != "editor" {
		t.Fatalf("tags = %#v", got.Tags)
	}
}

func TestMemoryManager_SedimentKnowledgeWithTypeCreatesTypedRecords(t *testing.T) {
	workspace, err := os.MkdirTemp("", "quantclaw-memory-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(workspace)

	manager, err := NewMemoryManager(workspace)
	if err != nil {
		t.Fatalf("NewMemoryManager: %v", err)
	}
	defer manager.Close()

	content := "User prefers concise answers. The deploy tool completed successfully."
	if err := manager.SedimentKnowledgeWithType(content, "session:phase3", MemoryTypeConversationSummary); err != nil {
		t.Fatalf("SedimentKnowledgeWithType: %v", err)
	}

	results, err := manager.Search("concise", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		keys, _ := manager.GetAllKeys()
		if len(keys) > 0 {
			content, _ := manager.Read(keys[0])
			t.Fatalf("expected typed sediment results, keys=%#v content=%q", keys, content)
		}
		t.Fatalf("expected typed sediment results, keys=%#v", keys)
	}

	foundPreference := false
	for _, result := range results {
		if result.Type == MemoryTypePreference && result.SourceKey == "session:phase3" {
			foundPreference = true
			break
		}
	}
	if !foundPreference {
		keys, _ := manager.GetAllKeys()
		t.Fatalf("expected preference record among sedimented results, got %#v (keys=%#v)", results, keys)
	}
}
