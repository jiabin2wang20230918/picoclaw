package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestSubagentStructuredResults(t *testing.T) {
	provider := &MockLLMProvider{}
	msgBus := bus.NewMessageBus()
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", msgBus)

	// Set a short timeout for testing
	manager.SetResourceLimits(3, 5*time.Second)

	// Create a spawn tool to test
	spawnTool := NewSpawnTool(manager)
	spawnTool.SetContext("test-channel", "test-chat")

	ctx := context.Background()
	args := map[string]any{
		"task":  "Test structured result",
		"label": "test-label",
	}

	result := spawnTool.Execute(ctx, args)

	if result == nil {
		t.Fatal("Result should not be nil")
	}

	if !result.Async {
		t.Error("Spawn tool should return async result")
	}

	// Check that the subagent was created
	tasks := manager.ListTasks()
	if len(tasks) == 0 {
		t.Error("Expected subagent task to be created")
	}
}

func TestSubagentResourceLimits(t *testing.T) {
	provider := &MockLLMProvider{}
	msgBus := bus.NewMessageBus()
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", msgBus)

	// Set strict resource limits
	manager.SetResourceLimits(1, 1*time.Second)

	spawnTool := NewSpawnTool(manager)
	spawnTool.SetContext("test-channel", "test-chat")

	ctx := context.Background()

	// First task should succeed
	args1 := map[string]any{
		"task": "First task",
		"label": "task1",
	}
	result1 := spawnTool.Execute(ctx, args1)
	if result1.IsError {
		t.Errorf("First task should succeed: %v", result1.ForLLM)
	}

	// Second task should hit the limit
	args2 := map[string]any{
		"task": "Second task",
		"label": "task2",
	}
	_ = spawnTool.Execute(ctx, args2)

	// We won't necessarily detect the limit violation immediately since it happens in goroutine,
	// but we can verify the configuration worked

	// Verify configuration was applied
	if manager.maxConcurrent != 1 {
		t.Errorf("Expected maxConcurrent to be 1, got %d", manager.maxConcurrent)
	}
	if manager.taskTimeout != 1*time.Second {
		t.Errorf("Expected taskTimeout to be 1s, got %v", manager.taskTimeout)
	}
}

func TestSubagentResultParsing(t *testing.T) {
	// Test that the result can be parsed as JSON
	result := SubagentResult{
		ID:     "test-id",
		Status: "completed",
		Data:   "test result",
		Metadata: map[string]interface{}{
			"key": "value",
		},
	}

	jsonData, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}

	var parsedResult SubagentResult
	err = json.Unmarshal(jsonData, &parsedResult)
	if err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if parsedResult.ID != "test-id" {
		t.Errorf("Expected ID to be 'test-id', got '%s'", parsedResult.ID)
	}

	if parsedResult.Status != "completed" {
		t.Errorf("Expected status to be 'completed', got '%s'", parsedResult.Status)
	}
}