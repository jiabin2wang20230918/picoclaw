# WeClaw ACP Integration Guide

This document explains how to configure and use the ACP (Agent Control Protocol) integration between WeClaw and QuantClaw.

## Overview

WeClaw acts as the WeChat protocol handler, connecting to various AI agents via different protocols. QuantClaw now supports the ACP protocol, allowing seamless integration with WeChat through WeClaw.

## Prerequisites

- WeClaw installed and configured
- QuantClaw built with ACP support (via `quantclaw-acp` binary)

## Configuration Steps

### 1. Build QuantClaw with ACP Support

```bash
# Build the ACP binary
make build-acp

# The binary will be available at:
# build/quantclaw-acp (symlink)
# build/quantclaw-acp-linux-amd64 (actual binary)
```

### 2. Configure WeClaw

Add the following configuration to your `~/.weclaw/config.json`:

```json
{
  "default_agent": "quantclaw",
  "agents": {
    "quantclaw": {
      "type": "acp",
      "command": "/path/to/quantclaw-acp",
      "args": ["--config", "/path/to/quantclaw/config.json"],
      "env": {
        "OPENAI_API_KEY": "your-openai-api-key",
        "QUANTCLAW_WORKSPACE": "/path/to/quantclaw/workspace"
      },
      "model": "gpt-4o-mini"
    }
  }
}
```

### 3. Prepare QuantClaw Configuration

Create or update your QuantClaw configuration file (e.g., `~/.quantclaw/config.json`):

```json
{
  "model_list": [
    {
      "model_name": "gpt-4o-mini",
      "model": "openai/gpt-4o-mini",
      "api_base": "https://api.openai.com/v1",
      "api_key": "your-openai-api-key"
    }
  ],
  "agents": {
    "defaults": {
      "model": "gpt-4o-mini",
      "workspace": "~/.quantclaw/workspace",
      "max_tool_iterations": 35,
      "send_progress": true,
      "send_tool_hints": false
    }
  },
  "tools": {
    "exec": {
      "enabled": true,
      "allow_patterns": ["/safe/path/**"]
    },
    "web": {
      "brave": {
        "enabled": false
      },
      "tavily": {
        "enabled": false
      }
    }
  }
}
```

### 4. Start the Services

1. First, start WeClaw (this will automatically start the QuantClaw ACP agent):

```bash
weclaw start
```

2. On first start, WeClaw will show a QR code. Scan this with WeChat to log in.

## How It Works

1. WeChat user sends a message to the connected WeChat account
2. WeClaw receives the message and routes it to the QuantClaw agent
3. WeClaw starts the `quantclaw-acp` binary as a subprocess using the ACP protocol
4. QuantClaw processes the message using its configured AI provider and tools
5. QuantClaw sends the response back to WeClaw via the ACP protocol
6. WeClaw forwards the response back to the WeChat user

## Troubleshooting

### Common Issues

- **"Command not found"**: Verify the path to `quantclaw-acp` is correct in the WeClaw config
- **Authentication failures**: Ensure API keys are properly configured in both WeClaw and QuantClaw configs
- **Connection issues**: Check that both services are running and properly configured

### Logging

- WeClaw logs: `~/.weclaw/weclaw.log`
- QuantClaw logs: Will be integrated into WeClaw logs when running in ACP mode

## Advanced Configuration

### Custom Models

To use different models, update the model configuration in your QuantClaw config and ensure it matches in the WeClaw config:

```json
{
  "agents": {
    "quantclaw": {
      "type": "acp",
      "command": "/path/to/quantclaw-acp",
      "args": ["--config", "/path/to/quantclaw/config.json"],
      "model": "claude-sonnet-4.6"  // This should match a model in your QuantClaw config
    }
  }
}
```

### Environment Variables

You can pass environment variables to QuantClaw through the WeClaw configuration:

```json
{
  "agents": {
    "quantclaw": {
      "type": "acp",
      "command": "/path/to/quantclaw-acp",
      "args": [],
      "env": {
        "OPENAI_API_KEY": "your-api-key",
        "ANTHROPIC_API_KEY": "your-anthropic-key",
        "QUANTCLAW_WORKSPACE": "/custom/workspace/path"
      }
    }
  }
}
```

## Performance Considerations

- ACP protocol is efficient as it reuses the QuantClaw process for multiple conversations
- Sessions are maintained per WeChat user (identified by conversation ID)
- Tool execution permissions are handled automatically via ACP protocol

## Security

- The ACP protocol securely manages tool permissions
- Execution tools are subject to QuantClaw's safety restrictions
- API keys should be stored securely and not shared