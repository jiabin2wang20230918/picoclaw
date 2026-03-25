// Package weclaw provides integration between PicoClaw and weclaw
// to enable WeChat connectivity through the weclaw bridge.
package weclaw

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/config"
)

// Integration manages the integration between PicoClaw and weclaw
type Integration struct {
	adapter *agent.PicoClawWeclawAdapter
	service *agent.WeclawHTTPService
	config  *config.Config
}

// NewIntegration creates a new PicoClaw-weclaw integration
func NewIntegration(picoclawLoop *agent.AgentLoop, cfg *config.Config) *Integration {
	// Create the adapter
	defaultModel := "gpt-4o-mini" // Default fallback
	if cfg.Agents.Defaults.Model != "" {
		defaultModel = cfg.Agents.Defaults.Model
	}

	adapter := agent.NewPicoClawWeclawAdapter(picoclawLoop, defaultModel)

	// Create the HTTP service
	service := agent.NewWeclawHTTPService(adapter, cfg)

	return &Integration{
		adapter: adapter,
		service: service,
		config:  cfg,
	}
}

// Start starts the weclaw integration service
func (i *Integration) Start(ctx context.Context) error {
	if i.config != nil {
		// Check if weclaw service is enabled in config
		// Note: This would require adding weclaw config to the main config struct
		// For now, we'll assume it should start if this method is called
	}

	return i.service.Start(ctx)
}

// Stop stops the weclaw integration service
func (i *Integration) Stop(ctx context.Context) error {
	return i.service.Stop(ctx)
}

// GetAdapter returns the weclaw adapter for direct access
func (i *Integration) GetAdapter() *agent.PicoClawWeclawAdapter {
	return i.adapter
}

// GetInfo returns information about the integration
func (i *Integration) GetInfo() map[string]interface{} {
	info := i.adapter.Info()
	return map[string]interface{}{
		"name":     info.Name,
		"type":     info.Type,
		"model":    info.Model,
		"command":  info.Command,
		"status":   "running",
		"features": []string{"chat", "session_management"},
	}
}

// ValidateConfig checks if the configuration is valid for weclaw integration
func ValidateConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("configuration cannot be nil")
	}

	// Check if default agent is configured
	defaultAgent := cfg.Agents.Defaults
	if defaultAgent.Model == "" {
		return fmt.Errorf("default agent model must be configured")
	}

	return nil
}