package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// PicoClawACP implements the ACP (Agent Control Protocol) interface for PicoClaw
type PicoClawACP struct {
	loop    *AgentLoop
	config  *config.Config
	model   string

	mu       sync.Mutex
	started  bool
	nextID   atomic.Int64
	sessions map[string]string // conversationID -> sessionID

	// pending tracks in-flight JSON-RPC requests
	pendingMu sync.Mutex
	pending   map[int64]chan *rpcResponse

	// stdin/stdout for ACP communication
	stdin  io.Reader
	stdout io.Writer
}

// AgentInfo holds metadata about an agent for logging/debugging
type AgentInfo struct {
	Name    string // e.g. "picoclaw", "claude-acp"
	Type    string // e.g. "acp", "cli", "http"
	Model   string // e.g. "gpt-4o-mini", "sonnet"
	Command string // binary name, e.g. "picoclaw-acp"
	PID     int    // subprocess PID (0 if not applicable)
}

// ACP Protocol Types
type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
}

type clientCapabilities struct {
	FS *fsCapabilities `json:"fs,omitempty"`
}

type fsCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type newSessionParams struct {
	Cwd        string        `json:"cwd"`
	McpServers []interface{} `json:"mcpServers"`
}

type newSessionResult struct {
	SessionID string `json:"sessionId"`
}

type promptParams struct {
	SessionID string        `json:"sessionId"`
	Prompt    []promptEntry `json:"prompt"`
}

type promptEntry struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
}

type sessionUpdateParams struct {
	SessionID string        `json:"sessionId"`
	Update    sessionUpdate `json:"update"`
}

type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content,omitempty"`
	Type          string          `json:"type,omitempty"`
	Text          string          `json:"text,omitempty"`
}

// NewPicoClawACP creates a new PicoClaw ACP agent
func NewPicoClawACP(picoclawLoop *AgentLoop, cfg *config.Config) *PicoClawACP {
	return &PicoClawACP{
		loop:     picoclawLoop,
		config:   cfg,
		model:    cfg.Agents.Defaults.Model,
		sessions: make(map[string]string),
		pending:  make(map[int64]chan *rpcResponse),
		stdin:    os.Stdin,
		stdout:   os.Stdout,
	}
}

// Start initializes the ACP protocol communication
func (a *PicoClawACP) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return nil
	}

	// Initialize communication streams
	a.stdin = os.Stdin
	a.stdout = os.Stdout

	// Initialize session map
	a.sessions = make(map[string]string)

	a.started = true
	log.Println("[picoclaw-acp] ACP agent started")
	return nil
}

// Stop terminates the ACP communication
func (a *PicoClawACP) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.started {
		return
	}

	a.started = false
	log.Println("[picoclaw-acp] ACP agent stopped")
}

// Run starts the ACP protocol loop and handles communication with weclaw
func (a *PicoClawACP) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start ACP agent
	if err := a.Start(ctx); err != nil {
		return fmt.Errorf("failed to start ACP agent: %w", err)
	}

	log.Println("[picoclaw-acp] ACP agent initialized")

	// Keep running until stdin is closed - this is the main loop
	scanner := bufio.NewScanner(a.stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var msg rpcRequest
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			// Check if it's a response/notification instead of a request
			var respMsg rpcResponse
			if err2 := json.Unmarshal([]byte(line), &respMsg); err2 != nil {
				log.Printf("[picoclaw-acp] failed to parse message as request or response: %v (original request err: %v)", err2, err)
				continue
			}

			// Handle as response/notification
			a.handleResponseOrNotification(&respMsg)
			continue
		}

		// Handle the incoming request
		go a.handleRequest(&msg)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[picoclaw-acp] read error: %v", err)
	}

	log.Println("[picoclaw-acp] ACP main loop ended")
	return nil
}

// handleResponseOrNotification processes incoming responses or notifications
func (a *PicoClawACP) handleResponseOrNotification(msg *rpcResponse) {
	// Handle responses to our requests
	if msg.ID != nil && msg.Method == "" {
		a.pendingMu.Lock()
		ch, ok := a.pending[*msg.ID]
		a.pendingMu.Unlock()
		if ok {
			ch <- msg
		}
		return
	}

	// Handle notifications from client
	switch msg.Method {
	case "initialized":
		log.Println("[picoclaw-acp] received initialized notification")
	case "session/new":
		log.Println("[picoclaw-acp] received session/new notification - creating new session context")
		// Parse params from the session/new notification
		var params newSessionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			log.Printf("[picoclaw-acp] failed to parse session/new params: %v", err)
			return
		}

		// Generate a new session ID based on the notification
		sessionID := fmt.Sprintf("picoclaw-session-%d", time.Now().UnixNano())

		// Store the session context if needed
		log.Printf("[picoclaw-acp] created session context: %s", sessionID)
	case "initialize":
		log.Println("[picoclaw-acp] received initialize notification")
		// Handle initialize as notification (rare case, but handle it)
		// Usually initialize is a request with ID, but in some ACP variants it might be a notification
		// In this case, we don't send a response as it's a notification
	default:
		log.Printf("[picoclaw-acp] unhandled method: %s", msg.Method)
	}
}

// handleRequest processes incoming RPC requests
func (a *PicoClawACP) handleRequest(req *rpcRequest) {
	var result interface{}
	var rpcErr *rpcError
	var err error

	switch req.Method {
	case "initialize":
		// Convert params to json.RawMessage
		paramsBytes, _ := json.Marshal(req.Params)
		var paramsRaw json.RawMessage = paramsBytes
		result, err = a.handleInitialize(paramsRaw)
	case "session/new":
		// Convert params to json.RawMessage
		paramsBytes, _ := json.Marshal(req.Params)
		var paramsRaw json.RawMessage = paramsBytes
		result, err = a.handleNewSession(paramsRaw)
	case "session/prompt":
		// Convert params to json.RawMessage
		paramsBytes, _ := json.Marshal(req.Params)
		var paramsRaw json.RawMessage = paramsBytes
		result, err = a.handlePrompt(paramsRaw)
	default:
		rpcErr = &rpcError{
			Code:    -32601,
			Message: fmt.Sprintf("method %s not found", req.Method),
		}
	}

	// Send response
	resp := &rpcResponse{
		JSONRPC: "2.0",
		ID:      &req.ID,
		Result:  nil,
		Error:   nil,
	}

	if err != nil {
		rpcErr = &rpcError{
			Code:    -32000,
			Message: err.Error(),
		}
		resp.Error = rpcErr
	} else {
		resultBytes, _ := json.Marshal(result)
		resp.Result = resultBytes
	}

	a.sendResponse(resp)
}

// handleInitialize handles the initialization handshake
func (a *PicoClawACP) handleInitialize(params json.RawMessage) (interface{}, error) {
	log.Println("[picoclaw-acp] handling initialize")

	var initParams initParams
	if err := json.Unmarshal(params, &initParams); err != nil {
		return nil, fmt.Errorf("invalid initialize params: %w", err)
	}

	// Return basic initialization response
	result := map[string]interface{}{
		"protocolVersion": 1,
		"serverInfo": map[string]string{
			"name":    "picoclaw-acp",
			"version": "1.0.0",
		},
		"capabilities": map[string]interface{}{
			"executeCommands": false,
			"readWriteFiles":  false,
		},
	}

	return result, nil
}

// handleNewSession creates a new session
func (a *PicoClawACP) handleNewSession(params json.RawMessage) (interface{}, error) {
	log.Println("[picoclaw-acp] handling session/new")

	var sessionParams newSessionParams
	if err := json.Unmarshal(params, &sessionParams); err != nil {
		return nil, fmt.Errorf("invalid session params: %w", err)
	}

	// Generate a new session ID
	sessionID := fmt.Sprintf("picoclaw-session-%d", time.Now().UnixNano())

	result := newSessionResult{
		SessionID: sessionID,
	}

	return result, nil
}

// handlePrompt processes a prompt request
func (a *PicoClawACP) handlePrompt(params json.RawMessage) (interface{}, error) {
	log.Println("[picoclaw-acp] handling session/prompt")

	var promptParams promptParams
	if err := json.Unmarshal(params, &promptParams); err != nil {
		return nil, fmt.Errorf("invalid prompt params: %w", err)
	}

	// Find the user's message from the prompt
	var userMessage string
	for _, entry := range promptParams.Prompt {
		if entry.Type == "text" && entry.Text != "" {
			userMessage = entry.Text
			break
		}
	}

	if userMessage == "" {
		return nil, fmt.Errorf("no user message in prompt")
	}

	log.Printf("[picoclaw-acp] received user message: %.100s", userMessage)

	// Process the message with PicoClaw
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Use a fixed session key based on the ACP session ID
	sessionKey := "acp:" + promptParams.SessionID

	log.Printf("[picoclaw-acp] starting processing with session key: %s", sessionKey)

	// For ACP, we may need to wait for tool execution results before returning
	// This will block until the full interaction (including any tool calls) is complete
	response, err := a.loop.ProcessDirectWithChannel(ctx, userMessage, sessionKey, "acp", promptParams.SessionID)
	if err != nil {
		log.Printf("[picoclaw-acp] failed to process message: %v", err)

		// Even if there's an error, we should still send an error notification
		errorUpdate := sessionUpdate{
			SessionUpdate: "agent_error",
			Type:          "error",
			Text:          fmt.Sprintf("Error processing request: %v", err),
		}

		updateParams := sessionUpdateParams{
			SessionID: promptParams.SessionID,
			Update:    errorUpdate,
		}

		// Send notification update
		if notifyErr := a.sendNotification("session/update", updateParams); notifyErr != nil {
			log.Printf("[picoclaw-acp] failed to send error notification: %v", notifyErr)
		}

		return nil, fmt.Errorf("failed to process message with PicoClaw: %w", err)
	}

	log.Printf("[picoclaw-acp] completed processing, response length: %d, response: %.200s", len(response), response)

	// Send the response as an update notification to stream the result
	update := sessionUpdate{
		SessionUpdate: "agent_message_chunk",
		Type:          "text",
		Text:          response,
	}

	updateParams := sessionUpdateParams{
		SessionID: promptParams.SessionID,
		Update:    update,
	}

	// Send notification update
	if err := a.sendNotification("session/update", updateParams); err != nil {
		log.Printf("[picoclaw-acp] failed to send response notification: %v", err)
		// Don't return error here as the response was processed successfully
	}

	// Return result for the prompt request
	result := promptResult{
		StopReason: "complete",
	}

	return result, nil
}

// sendResponse sends a JSON-RPC response
func (a *PicoClawACP) sendResponse(resp *rpcResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	_, err = fmt.Fprintf(a.stdout, "%s\n", data)
	if err != nil {
		return fmt.Errorf("write to stdout: %w", err)
	}

	return nil
}

// sendNotification sends a JSON-RPC notification (no ID)
func (a *PicoClawACP) sendNotification(method string, params interface{}) error {
	msg := struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	_, err = fmt.Fprintf(a.stdout, "%s\n", data)
	if err != nil {
		return fmt.Errorf("write notification to stdout: %w", err)
	}

	return nil
}


// Info returns metadata about this agent
func (a *PicoClawACP) Info() AgentInfo {
	return AgentInfo{
		Name:    "picoclaw",
		Type:    "acp",
		Model:   a.model,
		Command: "picoclaw-acp",
	}
}

// Chat sends a message to the agent and returns the response
func (a *PicoClawACP) Chat(ctx context.Context, conversationID string, message string) (string, error) {
	// For ACP, we handle Chat through the main Run loop
	// This is used when ACP is running as a subprocess
	// For direct API usage, we use the PicoClaw loop directly
	sessionKey := "agent:main:acp:" + conversationID

	return a.loop.ProcessDirectWithChannel(ctx, message, sessionKey, "acp", conversationID)
}

// ResetSession clears the existing session for the given conversationID
func (a *PicoClawACP) ResetSession(ctx context.Context, conversationID string) (string, error) {
	// In ACP mode, session reset happens naturally when a new session is created
	sessionKey := "agent:main:acp:" + conversationID

	// Just return the session key since ACP handles sessions differently
	return sessionKey, nil
}

// SetCwd changes the working directory for subsequent operations
func (a *PicoClawACP) SetCwd(cwd string) {
	// ACP CWD is managed by the client, but we can store it for context
	log.Printf("[picoclaw-acp] setting CWD to: %s", cwd)
}