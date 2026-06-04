// Example showing persistent storage using the lifecycle-based memory system
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/sipeed/quantclaw/pkg/agent/memory"
)

func main() {
	// Create a persistent workspace directory (this time we'll keep it)
	workspace := filepath.Join(os.Getenv("HOME"), ".quantclaw", "lifecycle-demo-workspace")

	// Create the workspace directory if it doesn't exist
	err := os.MkdirAll(workspace, 0755)
	if err != nil {
		log.Fatal("Failed to create workspace directory:", err)
	}

	fmt.Printf("QuantClaw Persistent Memory Lifecycle Demo\n")
	fmt.Printf("Workspace: %s\n\n", workspace)

	// Initialize the Memory-Like-A-Tree system with persistent storage
	memoryManager, err := memory.NewMemoryManager(workspace)
	if err != nil {
		log.Fatal("Failed to initialize memory system:", err)
	}
	defer memoryManager.Close()

	// Create some knowledge items that will persist
	fmt.Println("Creating persistent knowledge items with different lifecycle states...")

	// Create a "sprout" item (confidence 0.7) - newly learned concept
	err = memoryManager.Save("concept/machine_learning_intro",
		"Machine learning is a method of data analysis that automates analytical model building. "+
			"It is a branch of artificial intelligence based on the idea that systems can learn from data, "+
			"identify patterns and make decisions with minimal human intervention.")
	if err != nil {
		log.Printf("Error saving ML intro: %v", err)
	} else {
		err = memoryManager.UpdateConfidence("concept/machine_learning_intro", 0.7, "new_concept")
		if err != nil {
			log.Printf("Error updating confidence: %v", err)
		}
		fmt.Printf("✅ Created 'machine_learning_intro' as a sprout (confidence: 0.7)\n")
	}

	// Create a "green leaf" item (confidence 0.9) - important fundamental concept
	err = memoryManager.Save("concept/python_benefits",
		"Python is widely used for web development, data science, artificial intelligence, "+
			"automation, and scientific computing. Its simple syntax and readability make it "+
			"an excellent choice for beginners and experts alike.")
	if err != nil {
		log.Printf("Error saving Python benefits: %v", err)
	} else {
		err = memoryManager.UpdateConfidence("concept/python_benefits", 0.9, "core_concept")
		if err != nil {
			log.Printf("Error updating confidence: %v", err)
		}
		fmt.Printf("✅ Created 'python_benefits' as a green leaf (confidence: 0.9)\n")
	}

	// Create a "yellow leaf" item (confidence 0.6) - moderately useful information
	err = memoryManager.Save("tip/vscode_shortcuts",
		"VSCode shortcut: Ctrl+Shift+P opens command palette. "+
			"Ctrl+P to quick open files. Ctrl+Shift+L to select all occurrences of selected text.")
	if err != nil {
		log.Printf("Error saving VSCode tips: %v", err)
	} else {
		err = memoryManager.UpdateConfidence("tip/vscode_shortcuts", 0.6, "moderate_importance")
		if err != nil {
			log.Printf("Error updating confidence: %v", err)
		}
		fmt.Printf("✅ Created 'vscode_shortcuts' as a yellow leaf (confidence: 0.6)\n")
	}

	// Access the important concept to reinforce it
	fmt.Println("\nReinforcing important concepts...")
	_, err = memoryManager.Read("concept/python_benefits")
	if err != nil {
		log.Printf("Error reading python benefits: %v", err)
	} else {
		fmt.Printf("📚 Accessed important concept, increasing its confidence\n")
	}

	// Show current state distribution
	fmt.Println("\nCurrent memory state distribution:")
	showStateDistribution(memoryManager)

	// Run maintenance to simulate passage of time
	fmt.Println("\nRunning periodic maintenance to simulate lifecycle progression...")
	err = memoryManager.RunPeriodicMaintenance()
	if err != nil {
		log.Printf("Error running maintenance: %v", err)
	} else {
		fmt.Printf("🔧 Maintenance completed\n")
	}

	// Show state after maintenance
	fmt.Println("\nMemory state distribution after maintenance:")
	showStateDistribution(memoryManager)

	// Demonstrate search capability
	fmt.Println("\nSearching for 'learning':")
	results, err := memoryManager.Search("learning", 5)
	if err != nil {
		log.Printf("Search error: %v", err)
	} else {
		for _, result := range results {
			confidence, _ := memoryManager.GetConfidence(result.Key)
			fmt.Printf("🔍 Found '%s' (confidence: %.2f, relevance: %.2f)\n",
				result.Key, confidence, result.Score)
		}
	}

	fmt.Println("\n✅ Memory items have been saved to persistent storage!")
	fmt.Printf("📁 You can find the database at: %s/memory.db\n", workspace)
	fmt.Printf("📋 Long-term memory at: %s/MEMORY.md\n", workspace)
	fmt.Printf("📅 Daily notes in: %s/memory/\n", workspace)
	fmt.Println("\n💡 This demonstrates the persistent lifecycle-based memory system.")
}

func showStateDistribution(mm *memory.MemoryManager) {
	// Get sprout items (0.7-0.8 confidence)
	sprouts, _, err := mm.GetSproutItems(10)
	if err != nil {
		log.Printf("Error getting sprouts: %v", err)
	} else {
		fmt.Printf("  🌱 Sprouts (0.7-0.8): %d items\n", len(sprouts))
	}

	// Get green leaf items (0.8-1.0 confidence)
	greenLeaves, _, err := mm.GetGreenLeafItems(10)
	if err != nil {
		log.Printf("Error getting green leaves: %v", err)
	} else {
		fmt.Printf("  🌿 Green Leaves (0.8-1.0): %d items\n", len(greenLeaves))
	}

	// Get yellow leaf items (0.5-0.7 confidence)
	yellowLeaves, _, err := mm.GetYellowLeafItems(10)
	if err != nil {
		log.Printf("Error getting yellow leaves: %v", err)
	} else {
		fmt.Printf("  🍂 Yellow Leaves (0.5-0.7): %d items\n", len(yellowLeaves))
	}

	// Get dead leaf items (0.3-0.5 confidence)
	deadLeaves, _, err := mm.GetDeadLeafItems(10)
	if err != nil {
		log.Printf("Error getting dead leaves: %v", err)
	} else {
		fmt.Printf("  🍁 Dead Leaves (0.3-0.5): %d items\n", len(deadLeaves))
	}

	// Get soil items (<0.3 confidence)
	soilItems, _, err := mm.GetSoilItems(10)
	if err != nil {
		log.Printf("Error getting soil items: %v", err)
	} else {
		fmt.Printf("  🪨 Soil (<0.3): %d items\n", len(soilItems))
	}
}
