package bus

type InboundMessage struct {
	Channel    string            `json:"channel"`
	SenderID   string            `json:"sender_id"`
	ChatID     string            `json:"chat_id"`
	Content    string            `json:"content"`
	Media      []string          `json:"media,omitempty"`
	SessionKey string            `json:"session_key"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type MessageType string

const (
	MessageTypeNormal      MessageType = "normal"
	MessageTypeProgress    MessageType = "progress"
	MessageTypeToolResult  MessageType = "tool_result"
	MessageTypeStreaming   MessageType = "streaming"
)

type OutboundMessage struct {
	Channel     string            `json:"channel"`
	ChatID      string            `json:"chat_id"`
	Content     string            `json:"content"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	MessageType MessageType       `json:"message_type,omitempty"`
	Progress    *float64          `json:"progress,omitempty"` // Optional progress percentage (0-100)
	Metadata    map[string]string `json:"metadata,omitempty"` // Additional metadata for the message
}

type Attachment struct {
	URL  string `json:"url"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

type MessageHandler func(InboundMessage) error

// SteeringMessage represents a user interruption during agent processing.
// When a steering message is received, the agent skips remaining tools
// and processes the user's new input.
type SteeringMessage struct {
	Channel    string `json:"channel"`
	ChatID     string `json:"chat_id"`
	Content    string `json:"content"`
	SessionKey string `json:"session_key"`
	Timestamp  int64  `json:"timestamp"`
}
