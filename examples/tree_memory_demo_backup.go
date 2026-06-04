// Example demonstrating the tree-like memory system for QuantClaw
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/sipeed/quantclaw/pkg/agent/memory"
	"github.com/sipeed/quantclaw/pkg/providers"
)

func main() {
	fmt.Println("QuantClaw Tree-like Memory System Demo")
	fmt.Println("=====================================")

	// Create a new memory manager with Memory-Like-A-Tree architecture
	memoryManager, err := memory.NewMemoryManager("./demo-workspace")
	if err != nil {
		log.Fatalf("Failed to create memory manager: %v", err)
	}
	defer memoryManager.Close()

	// Example 1: Store a conversation session
	fmt.Println("\n1. Storing a conversation session...")

	// Simulate a conversation history
	messages := []providers.Message{
		{Role: "user", Content: "Hi, I need help with my Python code"},
		{Role: "assistant", Content: "Sure, what Python issue are you facing?"},
		{Role: "user", Content: "I'm trying to parse JSON but getting errors"},
		{Role: "assistant", Content: "Can you share the code snippet? I can help you debug it."},
		{Role: "user", Content: "Here's my code: import json; data = json.loads('{\"key\": \"value\"}')"},
		{Role: "assistant", Content: "Your code looks fine. Are you sure the JSON string is valid?"},
	}

	// Store knowledge from the session
	sessionKey := "demo-session-1"
	for i, msg := range messages {
		key := fmt.Sprintf("%s:message-%d", sessionKey, i)
		err := memoryManager.Save(key, msg.Content)
		if err != nil {
			log.Printf("Error storing message: %v", err)
		}
	}

	err = memoryManager.SedimentKnowledge("User needed help with JSON parsing in Python", sessionKey)
	if err != nil {
		log.Printf("Error storing knowledge from session: %v", err)
	}

	// Example 2: Retrieve relevant history
	fmt.Println("\n2. Retrieving relevant history...")
	results, err := memoryManager.Search("Python JSON", 5)
	if err != nil {
		log.Printf("Error retrieving history: %v", err)
	} else {
		fmt.Printf("Found %d relevant items:\n", len(results))
		for i, result := range results {
			fmt.Printf("  %d. [%s] %s\n", i+1, result.Key, truncateString(result.Content, 50))
		}
	}

	// Example 3: Show traditional memory functionality still works
	fmt.Println("\n3. Traditional memory functionality still available...")

	// Write to long-term memory
	longTermContent := fmt.Sprintf("# Demo Knowledge\nExtracted at %s\n- Python JSON parsing is straightforward\n- Always validate input JSON strings\n", time.Now().Format(time.RFC3339))
	err = memoryManager.WriteLongTerm(longTermContent)
	if err != nil {
		log.Printf("Error writing to long-term memory: %v", err)
	}

	// Read from long-term memory
	readContent := memoryManager.ReadLongTerm()
	fmt.Printf("Long-term memory content (truncated): %s\n", truncateString(readContent, 100))

	// Append to daily notes
	err = memoryManager.AppendToday(fmt.Sprintf("- Demo session completed at %s\n", time.Now().Format("15:04")))
	if err != nil {
		log.Printf("Error appending to daily notes: %v", err)
	}

	// Read today's notes
	todayNotes := memoryManager.ReadToday()
	fmt.Printf("Today's notes: %s\n", todayNotes)

	// Example 4: Show memory compaction
	fmt.Println("\n4. Demonstrating memory compaction...")
	err = memoryManager.RunPeriodicMaintenance()
	if err != nil {
		log.Printf("Error running maintenance: %v", err)
	} else {
		fmt.Println("Memory maintenance completed successfully.")
	}

	// Example 5: Get memory statistics
	fmt.Println("\n5. Getting memory statistics...")
	stats, err := memoryManager.GetCurrentStats()
	if err != nil {
		log.Printf("Error getting stats: %v", err)
	} else {
		fmt.Printf("Memory statistics: %+v\n", stats)
	}

	fmt.Println("\nDemo completed! Memory-Like-A-Tree system is operational.")
}

func truncateString(str string, num int) string {
	if len(str) <= num {
		return str
	}
	return str[0:num] + "..."
}
