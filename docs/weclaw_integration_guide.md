# PicoClaw + weclaw Integration Guide

## Overview

This guide explains how to integrate PicoClaw with weclaw, allowing you to connect WeChat users to your PicoClaw AI agents.

The integration works by running PicoClaw as an HTTP agent service that weclaw can connect to. This is different from the original approach of directly connecting PicoClaw to WeChat, which was incorrect since weclaw uses Tencent's internal iLink API.

## Architecture

```
WeChat → weclaw → HTTP API → PicoClaw → AI Models
```

## Setup Instructions

### Step 1: Configure PicoClaw for weclaw Integration

You'll need to add a new configuration section to your PicoClaw config.json to enable the weclaw HTTP service:

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.picoclaw/workspace",
      "restrict_to_workspace": true,
      "provider": "openai",
      "model": "gpt-4o-mini",
      "max_tokens": 4096,
      "max_tool_iterations": 35,
      "send_progress": true,
      "send_tool_hints": true
    }
  },
  "weclaw_service": {
    "enabled": true,
    "port": 18080,
    "address": "127.0.0.1"
  }
}
```

### Step 2: Run PicoClaw with weclaw Service

Start PicoClaw with the weclaw service enabled. You can do this by extending the main application:

```go
package main

import (
    "context"
    "log"

    "github.com/sipeed/picoclaw/pkg/agent"
    "github.com/sipeed/picoclaw/pkg/config"
)

func main() {
    // Load PicoClaw configuration
    cfg, err := config.LoadConfig("~/.picoclaw/config.json")
    if err != nil {
        log.Fatal("Failed to load config:", err)
    }

    // Initialize your PicoClaw AgentLoop
    loop := agent.NewAgentLoop(cfg, /* message bus */, /* provider */)

    // Create the weclaw adapter
    adapter := agent.NewPicoClawWeclawAdapter(loop, cfg.Agents.Defaults.Model)

    // Create and start the HTTP service
    service := agent.NewWeclawHTTPService(adapter, cfg)

    ctx := context.Background()
    if err := service.Start(ctx); err != nil {
        log.Fatal("Failed to start weclaw service:", err)
    }

    // Keep the service running
    select {}
}
```

### Step 3: Configure weclaw to Use PicoClaw

Once PicoClaw is running with the weclaw service, configure weclaw to connect to it. In your weclaw configuration (`~/.weclaw/config.json`):

```json
{
  "default_agent": "picoclaw-http",
  "agents": {
    "picoclaw-http": {
      "type": "http",
      "endpoint": "http://127.0.0.1:18080/weclaw/chat",
      "model": "picoclaw-main"
    }
  }
}
```

### Step 4: Start Both Services

1. Start PicoClaw with the weclaw HTTP service enabled
2. Start weclaw normally using `weclaw start`
3. Scan the QR code displayed by weclaw with WeChat to log in
4. Now WeChat messages will be processed by your PicoClaw agents!

## API Endpoints

The weclaw service exposes these endpoints:

- `POST /weclaw/chat` - Process chat messages
- `GET /weclaw/health` - Health check
- `GET /weclaw/info` - Service information

### Chat Endpoint Details

Request format:
```json
{
  "conversation_id": "unique_conversation_identifier",
  "message": "user message content",
  "model": "optional model override"
}
```

Response format:
```json
{
  "success": true,
  "message": "response from PicoClaw",
  "model": "model_used",
  "timestamp": 1234567890
}
```

## Troubleshooting

1. **Connection Issues**: Make sure both PicoClaw and weclaw are running and can reach each other
2. **Configuration Issues**: Verify the endpoint URLs are correctly configured
3. **Authentication**: If you encounter authentication issues, check that both services are properly configured

## Security Considerations

- The service binds to localhost by default (127.0.0.1) for security
- Consider using additional authentication mechanisms for production deployments
- Monitor the API for unexpected usage patterns