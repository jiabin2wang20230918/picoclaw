package memory

import (
	"encoding/json"
	"strings"
	"time"
)

type MemoryType string

const (
	MemoryTypeFact                MemoryType = "fact"
	MemoryTypePreference          MemoryType = "preference"
	MemoryTypeArtifact            MemoryType = "artifact"
	MemoryTypeToolObservation     MemoryType = "tool_observation"
	MemoryTypeConversationSummary MemoryType = "conversation_summary"
)

type MemoryRecord struct {
	Key        string
	Content    string
	Type       MemoryType
	SourceKey  string
	Confidence float64
	Score      float64
	Tags       []string
	Reason     string
	UpdatedAt  time.Time
}

func normalizeMemoryType(memoryType MemoryType) MemoryType {
	switch memoryType {
	case MemoryTypeFact,
		MemoryTypePreference,
		MemoryTypeArtifact,
		MemoryTypeToolObservation,
		MemoryTypeConversationSummary:
		return memoryType
	default:
		return MemoryTypeArtifact
	}
}

func tagsToJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	data, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func tagsFromJSON(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}
