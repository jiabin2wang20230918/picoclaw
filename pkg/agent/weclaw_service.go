package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// WeclawHTTPService provides HTTP endpoints for weclaw to interact with PicoClaw
type WeclawHTTPService struct {
	adapter *PicoClawWeclawAdapter
	server  *http.Server
	config  *config.Config
}

// ChatRequest represents the request structure from weclaw
type ChatRequest struct {
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
	Model          string `json:"model,omitempty"`
}

// ChatResponse represents the response structure to weclaw
type ChatResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	Model     string `json:"model,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// NewWeclawHTTPService creates a new HTTP service for weclaw integration
func NewWeclawHTTPService(adapter *PicoClawWeclawAdapter, cfg *config.Config) *WeclawHTTPService {
	service := &WeclawHTTPService{
		adapter: adapter,
		config:  cfg,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/weclaw/chat", service.handleChat)
	mux.HandleFunc("/weclaw/health", service.handleHealth)
	mux.HandleFunc("/weclaw/info", service.handleInfo)

	service.server = &http.Server{
		Addr:    ":18080", // Default port, can be configured
		Handler: mux,
	}

	return service
}

// Start starts the HTTP service
func (s *WeclawHTTPService) Start(ctx context.Context) error {
	// Run server in a goroutine
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// In a real implementation, you'd want to handle this error properly
			fmt.Printf("Weclaw HTTP service error: %v\n", err)
		}
	}()

	return nil
}

// Stop stops the HTTP service
func (s *WeclawHTTPService) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// handleChat handles chat requests from weclaw
func (s *WeclawHTTPService) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.sendErrorResponse(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.sendErrorResponse(w, "Invalid JSON in request", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Message == "" {
		s.sendErrorResponse(w, "Message is required", http.StatusBadRequest)
		return
	}

	if req.ConversationID == "" {
		req.ConversationID = "default" // Use a default conversation ID if none provided
	}

	// Process the message with the adapter
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	response, err := s.adapter.Chat(ctx, req.ConversationID, req.Message)
	if err != nil {
		s.sendErrorResponse(w, fmt.Sprintf("Failed to process message: %v", err), http.StatusInternalServerError)
		return
	}

	// Send successful response
	resp := ChatResponse{
		Success:   true,
		Message:   response,
		Model:     s.adapter.Info().Model,
		Timestamp: time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleHealth handles health check requests
func (s *WeclawHTTPService) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"status":    "healthy",
		"service":   "picoclaw-weclaw-adapter",
		"timestamp": time.Now().Unix(),
		"model":     s.adapter.Info().Model,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleInfo handles info requests
func (s *WeclawHTTPService) handleInfo(w http.ResponseWriter, r *http.Request) {
	info := s.adapter.Info()
	resp := map[string]interface{}{
		"name":     info.Name,
		"type":     info.Type,
		"model":    info.Model,
		"command":  info.Command,
		"pid":      info.PID,
		"status":   "running",
		"features": []string{"chat", "session_management"},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// sendErrorResponse sends an error response to the client
func (s *WeclawHTTPService) sendErrorResponse(w http.ResponseWriter, errMsg string, statusCode int) {
	resp := ChatResponse{
		Success:   false,
		Error:     errMsg,
		Timestamp: time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(resp)
}