# QuantClaw Runtime Architecture

This document describes the current QuantClaw runtime architecture as implemented in the repository today.

It intentionally documents the active execution path, not earlier design drafts. In particular:

- The main runtime centers on `AgentLoop`
- Message flow is coordinated through `MessageBus`
- Session compaction and message building are partially factored into helper components
- Fine-grained permissions and a fully separated orchestrator/executor pipeline are not the default runtime path

## Top-Level Structure

The runtime is composed of these main layers:

- **CLI / Gateway entrypoints**: start the process in direct or long-running mode
- **MessageBus**: connects inbound messages, outbound replies, and steering/interruption signals
- **AgentRegistry + RouteResolver**: choose which agent instance handles a message
- **AgentInstance**: holds model, workspace, tools, session storage, and context builder for one agent
- **AgentLoop**: the core message-processing loop and tool-call iteration engine
- **Provider layer**: sends message arrays and tool schemas to the configured LLM backend
- **Tool layer**: executes filesystem, shell, web, browser, cron, spawn, and device-related actions
- **Persistence layer**: stores session history, state, and long-term memory
- **Integration services**: channels, heartbeat, cron, devices, health server, and voice transcription

## Main Execution Path

The primary production path is:

1. A message enters from CLI, a chat channel, cron, heartbeat, or a system/subagent callback.
2. The message is published to `MessageBus`.
3. `AgentLoop.Run()` consumes the inbound message.
4. Routing resolves the target agent and session key.
5. The loop loads session history and summary, then retrieves relevant long-term memory.
6. The loop builds the provider message array from:
   - system prompt
   - bootstrap files from workspace
   - available tool summaries
   - available skill summaries
   - current session history
   - long-term memory search results
   - current user input
7. The configured provider is called with tool definitions.
8. If the model returns tool calls, `ToolHandler` executes them and appends tool-result messages.
9. The loop repeats until the model returns a final assistant answer or reaches iteration limits.
10. The final assistant message is saved to session storage, optionally summarized/compacted, and published outbound.

## Entry Modes

### `quantclaw agent`

`cmd/quantclaw/cmd_agent.go`

- Direct local interaction
- Can run single-shot or interactive mode
- Uses `ProcessDirect()` to feed the loop without starting channels

### `quantclaw gateway`

`cmd/quantclaw/cmd_gateway.go`

- Long-running process for real deployments
- Starts:
  - `MessageBus`
  - `AgentLoop`
  - channel manager
  - cron service
  - heartbeat service
  - device service
  - health server
  - optional voice transcription for some channels

## Core Runtime Components

### MessageBus

`pkg/bus/bus.go`

Provides three queues:

- `inbound`: user/system messages headed into the agent
- `outbound`: replies/progress messages headed to channels
- `steering`: interruption/redirect messages for active sessions

This is the glue between channels and the agent loop.

### AgentRegistry and Routing

`pkg/agent/registry.go`
`pkg/routing/route.go`

Responsibilities:

- create agent instances from config
- create an implicit `main` agent if `agents.list` is empty
- resolve agent bindings and session keys
- enforce subagent allowlists

Routing priority is:

- peer
- parent peer
- guild
- team
- account
- channel wildcard
- default agent

### AgentInstance

`pkg/agent/instance.go`

Each agent instance owns:

- resolved workspace
- resolved primary model and fallback models
- tool registry
- session storage
- context builder
- subagent config
- compaction config

This is the unit of execution selected by routing.

### AgentLoop

`pkg/agent/loop.go`

`AgentLoop` is the real runtime center of QuantClaw today.

It handles:

- message intake from the bus
- command shortcuts such as `/ping` and `/reset`
- routing to the correct agent
- session/history loading
- memory retrieval
- message construction
- provider calls and fallback handling
- tool-call iteration
- output truncation continuation
- context-overflow recovery through compression
- final response persistence and publishing

## Context Construction

### ContextBuilder

`pkg/agent/context.go`

The baseline system prompt is built from:

- runtime metadata
- workspace paths
- available tool summaries
- bootstrap files from workspace:
  - `AGENT.md`
  - `SOUL.md`
  - `USER.md`
  - `IDENTITY.md`
- skill summaries
- memory access instructions

### MessageBuilder

`pkg/agent/message_builder.go`

This helper composes the final message list and prunes it to fit token limits.

In the current codebase it is a helper used by `AgentLoop`, not a separate runtime orchestrator.

### SessionManager wrapper

`pkg/agent/session_manager.go`

This wraps the lower-level JSON-backed session store and adds:

- convenience append helpers
- compression helpers that preserve tool-call/result relationships

## Session and Memory Persistence

### Sessions

`pkg/session/manager.go`

Sessions are persisted as JSON files under the workspace `sessions/` directory.

Stored data includes:

- user messages
- assistant messages
- assistant tool-call metadata
- tool results
- optional summary text

### State

`pkg/state/state.go`

Stores low-volume runtime state such as:

- last active channel
- last active chat ID

This is used by heartbeat and device notifications.

### Memory

`pkg/agent/memory/manager.go`

Long-term memory is backed by the Memory-Like-A-Tree subsystem.

Current usage in the main loop:

- search relevant memories before each turn
- sediment final responses into memory
- sediment selected tool outputs into memory
- migrate legacy `MEMORY.md` and recent daily notes into the new memory backend

## Tool Execution

### Tool Registry

`pkg/tools/registry.go`

Converts tools into provider-compatible schemas and executes them by name.

### ToolHandler

`pkg/agent/tool_handler.go`

Current behavior:

- normalizes tool calls
- validates tool arguments when supported
- executes tool calls concurrently
- preserves the original tool-call order in the returned tool result messages

### Tool Categories

Registered tools currently include combinations of:

- filesystem tools
- shell execution
- edit/append helpers
- web search and fetch
- browser/CDP tools
- message sending
- cron scheduling
- skill search/install
- spawn/subagent tools
- hardware tools (`i2c`, `spi`)

## Provider Layer

`pkg/providers/*`

The provider layer exposes one common `Chat()` interface.

Supported backends include:

- OpenAI-compatible HTTP providers
- Anthropic
- OpenRouter
- Gemini-compatible routes
- Zhipu / Qwen / DeepSeek / Groq / Moonshot / NVIDIA / others via config
- Claude CLI
- Codex CLI
- GitHub Copilot bridge

Fallback handling is implemented in the active runtime path and can retry across multiple configured candidates.

## Integrations Around the Core Loop

### Channels

`pkg/channels/*`

Supported integrations include Telegram, WhatsApp, Feishu, Discord, Slack, LINE, OneBot, WeCom, WeCom App, QQ, DingTalk, and MaixCam.

Channels publish inbound messages into `MessageBus` and subscribe to outbound messages from it.

### Cron

`pkg/cron/service.go`

Schedules one-time, interval, or cron-expression jobs and feeds work back into the agent.

### Heartbeat

`pkg/heartbeat/service.go`

Reads `HEARTBEAT.md` from the workspace on a schedule and invokes the agent without loading normal session history.

### Devices

`pkg/devices/service.go`

Currently used for device-event monitoring such as USB hotplug and notifying the last active user channel.

### Voice

`pkg/voice/transcriber.go`

Optional voice transcription is attached by the gateway to selected chat channels when Groq credentials are available.

## What Is Not Part of the Default Runtime

The repository contains some experimental or partially integrated abstractions, but they are not the primary execution path described above.

Examples:

- `pkg/agent/orchestrator/*`
- `pkg/agent/interfaces/*`
- `pkg/agent/permissions/*`
- `pkg/agent/router/*`
- `pkg/agent/execution/*`
- `pkg/agent/supervisor/*`

These packages are useful as implementation support or future refactoring targets, but the active runtime still flows through `AgentLoop`.

## Summary

QuantClaw's current architecture is best understood as:

- a bus-driven agent runtime
- with per-agent workspaces and sessions
- a central tool-call loop inside `AgentLoop`
- persistent short-term and long-term context
- and multiple external integration services layered around the same core

That is the architecture reflected by the code today.
