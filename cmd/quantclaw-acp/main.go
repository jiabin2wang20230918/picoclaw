package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sipeed/quantclaw/pkg/agent"
	"github.com/sipeed/quantclaw/pkg/bus"
	"github.com/sipeed/quantclaw/pkg/config"
	"github.com/sipeed/quantclaw/pkg/providers"
)

var (
	configFile = flag.String("config", "", "Configuration file path")
)

func main() {
	flag.Parse()

	// Load configuration
	cfg := &config.Config{}
	if *configFile != "" {
		var err error
		cfg, err = config.LoadConfig(*configFile)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	} else {
		// Try to load from default locations
		var err error
		homeDir, _ := os.UserHomeDir()
		configPath := filepath.Join(homeDir, ".quantclaw", "config.json")
		cfg, err = config.LoadConfig(configPath)
		if err != nil {
			// Create a minimal default config
			cfg = config.DefaultConfig()
			// Override the default model to be more generic
			cfg.Agents.Defaults.Model = "gpt-4o-mini"
		}
	}

	// Set up message bus
	msgBus := bus.NewMessageBus()

	// Find the default model in ModelList to get the correct configuration and actual model name
	var defaultModelConfig *config.ModelConfig
	defaultModelName := cfg.Agents.Defaults.Model

	// Look for the default model in the model list
	for i := range cfg.ModelList {
		if cfg.ModelList[i].ModelName == defaultModelName {
			defaultModelConfig = &cfg.ModelList[i]
			break
		}
	}

	if defaultModelConfig == nil {
		log.Fatalf("Default model '%s' not found in model_list", defaultModelName)
	}

	provider, actualModel, err := providers.CreateProviderFromConfig(defaultModelConfig)
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}

	// Update the default model in the config to use the actual model identifier for API calls
	// This ensures that AgentInstance uses the correct model name for API requests
	cfg.Agents.Defaults.Model = actualModel

	// Create agent loop
	loop := agent.NewAgentLoop(cfg, msgBus, provider)

	// Create ACP agent
	acpAgent := agent.NewQuantClawACP(loop, cfg)

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start ACP agent
	if err := acpAgent.Start(ctx); err != nil {
		log.Fatalf("Failed to start ACP agent: %v", err)
	}

	fmt.Fprint(os.Stderr, "[quantclaw-acp] ACP agent initialized\n")

	// Run the ACP protocol
	go func() {
		if err := acpAgent.Run(); err != nil {
			log.Printf("ACP agent error: %v", err)
		}
	}()

	// Wait for shutdown signal
	select {
	case sig := <-sigChan:
		log.Printf("Received signal: %s", sig)
	case <-ctx.Done():
		log.Println("Context cancelled")
	}

	cancel()
	acpAgent.Stop()
	log.Println("ACP agent stopped")
}
