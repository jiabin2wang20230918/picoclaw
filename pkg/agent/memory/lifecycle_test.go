package memory

import (
	"os"
	"testing"
)

func TestMemoryLifecycleModel(t *testing.T) {
	// Create a temporary workspace for the test
	workspace, err := os.MkdirTemp("", "quantclaw_test_*")
	if err != nil {
		t.Fatalf("Failed to create temporary workspace: %v", err)
	}
	defer os.RemoveAll(workspace)

	// Initialize the Memory-Like-A-Tree system
	memoryManager, err := NewMemoryManager(workspace)
	if err != nil {
		t.Fatalf("Failed to initialize memory system: %v", err)
	}
	defer memoryManager.Close()

	// Test creating items in different lifecycle states
	t.Run("CreateAndManageLifeCycleStates", func(t *testing.T) {
		// Create a "sprout" item (confidence 0.7)
		err = memoryManager.Save("test/sprout_item", "This is a sprout item")
		if err != nil {
			t.Errorf("Error saving sprout item: %v", err)
		}
		err = memoryManager.UpdateConfidence("test/sprout_item", 0.75, "initial_sprout")
		if err != nil {
			t.Errorf("Error updating sprout confidence: %v", err)
		}

		// Create a "green leaf" item (confidence 0.9)
		err = memoryManager.Save("test/green_leaf_item", "This is a green leaf item")
		if err != nil {
			t.Errorf("Error saving green leaf item: %v", err)
		}
		err = memoryManager.UpdateConfidence("test/green_leaf_item", 0.95, "important_green_leaf")
		if err != nil {
			t.Errorf("Error updating green leaf confidence: %v", err)
		}

		// Create a "yellow leaf" item (confidence 0.6)
		err = memoryManager.Save("test/yellow_leaf_item", "This is a yellow leaf item")
		if err != nil {
			t.Errorf("Error saving yellow leaf item: %v", err)
		}
		err = memoryManager.UpdateConfidence("test/yellow_leaf_item", 0.65, "moderate_yellow_leaf")
		if err != nil {
			t.Errorf("Error updating yellow leaf confidence: %v", err)
		}

		// Verify items are in correct lifecycle states
		sprouts, _, err := memoryManager.GetSproutItems(10)
		if err != nil {
			t.Errorf("Error getting sprout items: %v", err)
		}
		foundSprout := false
		for _, key := range sprouts {
			if key == "test/sprout_item" {
				foundSprout = true
				break
			}
		}
		if !foundSprout {
			t.Error("Sprout item not found in sprout state")
		}

		greenLeaves, _, err := memoryManager.GetGreenLeafItems(10)
		if err != nil {
			t.Errorf("Error getting green leaf items: %v", err)
		}
		foundGreenLeaf := false
		for _, key := range greenLeaves {
			if key == "test/green_leaf_item" {
				foundGreenLeaf = true
				break
			}
		}
		if !foundGreenLeaf {
			t.Error("Green leaf item not found in green leaf state")
		}

		yellowLeaves, _, err := memoryManager.GetYellowLeafItems(10)
		if err != nil {
			t.Errorf("Error getting yellow leaf items: %v", err)
		}
		foundYellowLeaf := false
		for _, key := range yellowLeaves {
			if key == "test/yellow_leaf_item" {
				foundYellowLeaf = true
				break
			}
		}
		if !foundYellowLeaf {
			t.Error("Yellow leaf item not found in yellow leaf state")
		}
	})

	t.Run("AccessIncreasesConfidence", func(t *testing.T) {
		// Read the sprout item to increase its confidence (should potentially move to green leaf)
		_, err := memoryManager.Read("test/sprout_item")
		if err != nil {
			t.Errorf("Error reading sprout item: %v", err)
		}

		// Check that confidence increased
		newConfidence, err := memoryManager.GetConfidence("test/sprout_item")
		if err != nil {
			t.Errorf("Error getting confidence after access: %v", err)
		}

		if newConfidence <= 0.75 {
			t.Errorf("Expected confidence to increase after access, got %f", newConfidence)
		}
	})

	t.Run("RunMaintenance", func(t *testing.T) {
		// Run maintenance to test the lifecycle transitions
		err := memoryManager.RunPeriodicMaintenance()
		if err != nil {
			t.Errorf("Error running periodic maintenance: %v", err)
		}

		// After maintenance, verify the system is still operational
		content, err := memoryManager.Read("test/green_leaf_item")
		if err != nil {
			t.Errorf("Error reading green leaf item after maintenance: %v", err)
		}
		if content == "" {
			t.Error("Content is empty after maintenance")
		}
	})
}