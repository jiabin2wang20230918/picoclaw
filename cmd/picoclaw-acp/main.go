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

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
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
		configPath := filepath.Join(homeDir, ".picoclaw", "config.json")
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

	// Create LLM provider
	if len(cfg.ModelList) == 0 {
		log.Fatalf("No models configured in config")
	}
	provider, _, err := providers.CreateProviderFromConfig(&cfg.ModelList[0]) // Use first model
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}

	// Create agent loop
	loop := agent.NewAgentLoop(cfg, msgBus, provider)

	// Create ACP agent
	acpAgent := agent.NewPicoClawACP(loop, cfg)

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start ACP agent
	if err := acpAgent.Start(ctx); err != nil {
		log.Fatalf("Failed to start ACP agent: %v", err)
	}

	fmt.Fprint(os.Stderr, "[picoclaw-acp] ACP agent initialized\n")

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