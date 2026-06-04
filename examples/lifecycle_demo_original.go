// Example demonstrating the new lifecycle-based memory management system
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/sipeed/quantclaw/pkg/agent/memory"
)

func main() {
	// Create a temporary workspace for the demo
	workspace, err := os.MkdirTemp("", "quantclaw_lifecycle_demo_*")
	if err != nil {
		log.Fatal("Failed to create temporary workspace:", err)
	}
	defer os.RemoveAll(workspace) // Clean up after demo

	fmt.Printf("QuantClaw Memory Lifecycle Demo\n")
	fmt.Printf("Workspace: %s\n\n", workspace)

	// Initialize the Memory-Like-A-Tree system
	memoryManager, err := memory.NewMemoryManager(workspace)
	if err != nil {
		log.Fatal("Failed to initialize memory system:", err)
	}
	defer memoryManager.Close()

	// Demonstrate the lifecycle model by creating various memory items
	fmt.Println("Creating knowledge items with different initial confidences...")

	// Create a "sprout" item (confidence 0.7)
	err = memoryManager.Save("concepts/golang_basics", "Go is a statically typed, compiled programming language.")
	if err != nil {
		log.Printf("Error saving golang_basics: %v", err)
	}
	err = memoryManager.UpdateConfidence("concepts/golang_basics", 0.7, "initial_knowledge")
	if err != nil {
		log.Printf("Error updating confidence: %v", err)
	}

	// Create a "green leaf" item (confidence 0.9)
	err = memoryManager.Save("concepts/web_development", "Web development involves creating websites and web applications.")
	if err != nil {
		log.Printf("Error saving web_development: %v", err)
	}
	err = memoryManager.UpdateConfidence("concepts/web_development", 0.9, "important_concept")
	if err != nil {
		log.Printf("Error updating confidence: %v", err)
	}

	// Create a "yellow leaf" item (confidence 0.6)
	err = memoryManager.Save("tips/hydrate", "Remember to drink water regularly throughout the day.")
	if err != nil {
		log.Printf("Error saving hydrate: %v", err)
	}
	err = memoryManager.UpdateConfidence("tips/hydrate", 0.6, "reminder")
	if err != nil {
		log.Printf("Error updating confidence: %v", err)
	}

	// Create a "dead leaf" item (confidence 0.4)
	err = memoryManager.Save("todo/yesterday", "Complete the report by EOD.")
	if err != nil {
		log.Printf("Error saving todo: %v", err)
	}
	err = memoryManager.UpdateConfidence("todo/yesterday", 0.4, "old_task")
	if err != nil {
		log.Printf("Error updating confidence: %v", err)
	}

	// Access the green leaf item to increase its confidence (moving to more stable green leaf state)
	fmt.Println("\nAccessing important concepts to increase their confidence...")
	content, err := memoryManager.Read("concepts/web_development")
	if err != nil {
		log.Printf("Error reading web_development: %v", err)
	} else {
		fmt.Printf("Read concept: %s\n", content[:min(len(content), 50)]+"...")
	}

	// Access the sprout item to help it grow to green leaf
	content, err = memoryManager.Read("concepts/golang_basics")
	if err != nil {
		log.Printf("Error reading golang_basics: %v", err)
	} else {
		fmt.Printf("Read concept: %s\n", content[:min(len(content), 50)]+"...")
	}

	// Display initial state distribution
	fmt.Println("\nInitial memory state distribution:")
	showStateDistribution(memoryManager)

	// Simulate time passing - let some decay occur
	fmt.Println("\nSimulating time passing and running maintenance...")
	err = memoryManager.RunPeriodicMaintenance()
	if err != nil {
		log.Printf("Error running maintenance: %v", err)
	}

	// Show state distribution after maintenance
	fmt.Println("\nMemory state distribution after maintenance:")
	showStateDistribution(memoryManager)

	// Show confidence levels of our sample items
	fmt.Println("\nDetailed view of sample items:")
	showItemDetails(memoryManager, "concepts/golang_basics", "Golang basics")
	showItemDetails(memoryManager, "concepts/web_development", "Web development")
	showItemDetails(memoryManager, "tips/hydrate", "Hydration tip")
	showItemDetails(memoryManager, "todo/yesterday", "Yesterday's todo")

	// Demonstrate search functionality with confidence scoring
	fmt.Println("\nSearching for 'development':")
	results, err := memoryManager.Search("development", 5)
	if err != nil {
		log.Printf("Search error: %v", err)
	} else {
		for _, result := range results {
			confidence, _ := memoryManager.GetConfidence(result.Key)
			fmt.Printf("- %s (confidence: %.2f, relevance: %.2f)\n", result.Key, confidence, result.Score)
		}
	}

	fmt.Println("\nLifecycle-based Memory Management demo completed!")
}

func showStateDistribution(mm *memory.MemoryManager) {
	// Get sprout items (0.7-0.8 confidence)
	sprouts, _, err := mm.GetSproutItems(10)
	if err != nil {
		log.Printf("Error getting sprouts: %v", err)
	} else {
		fmt.Printf("  🌱 Sprouts (0.7-0.8): %d items\n", len(sprouts))
		for _, key := range sprouts {
			conf, _ := mm.GetConfidence(key)
			fmt.Printf("    - %s (confidence: %.2f)\n", key, conf)
		}
	}

	// Get green leaf items (0.8-1.0 confidence)
	greenLeaves, _, err := mm.GetGreenLeafItems(10)
	if err != nil {
		log.Printf("Error getting green leaves: %v", err)
	} else {
		fmt.Printf("  🌿 Green Leaves (0.8-1.0): %d items\n", len(greenLeaves))
		for _, key := range greenLeaves {
			conf, _ := mm.GetConfidence(key)
			fmt.Printf("    - %s (confidence: %.2f)\n", key, conf)
		}
	}

	// Get yellow leaf items (0.5-0.8 confidence)
	yellowLeaves, _, err := mm.GetYellowLeafItems(10)
	if err != nil {
		log.Printf("Error getting yellow leaves: %v", err)
	} else {
		fmt.Printf("  🍂 Yellow Leaves (0.5-0.7): %d items\n", len(yellowLeaves))
		for _, key := range yellowLeaves {
			conf, _ := mm.GetConfidence(key)
			fmt.Printf("    - %s (confidence: %.2f)\n", key, conf)
		}
	}

	// Get dead leaf items (0.3-0.5 confidence)
	deadLeaves, _, err := mm.GetDeadLeafItems(10)
	if err != nil {
		log.Printf("Error getting dead leaves: %v", err)
	} else {
		fmt.Printf("  🍁 Dead Leaves (0.3-0.5): %d items\n", len(deadLeaves))
		for _, key := range deadLeaves {
			conf, _ := mm.GetConfidence(key)
			fmt.Printf("    - %s (confidence: %.2f)\n", key, conf)
		}
	}

	// Get soil items (<0.3 confidence)
	soilItems, _, err := mm.GetSoilItems(10)
	if err != nil {
		log.Printf("Error getting soil items: %v", err)
	} else {
		fmt.Printf("  🪨 Soil (<0.3): %d items\n", len(soilItems))
		for _, key := range soilItems {
			conf, _ := mm.GetConfidence(key)
			fmt.Printf("    - %s (confidence: %.2f)\n", key, conf)
		}
	}
}

func showItemDetails(mm *memory.MemoryManager, key, label string) {
	conf, err := mm.GetConfidence(key)
	if err != nil {
		fmt.Printf("  %s: Error getting confidence - %v\n", label, err)
		return
	}

	state := getConfidenceStateLabel(conf)
	fmt.Printf("  %s: %s (confidence: %.2f)\n", label, state, conf)
}

func getConfidenceStateLabel(confidence float64) string {
	switch {
	case confidence >= 0.8:
		return "🌿 Green Leaf (0.8-1.0)"
	case confidence >= 0.7:
		return "🌱 Sprout (0.7-0.8)"
	case confidence >= 0.5:
		return "🍂 Yellow Leaf (0.5-0.7)"
	case confidence >= 0.3:
		return "🍁 Dead Leaf (0.3-0.5)"
	default:
		return "🪨 Soil (<0.3)"
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
