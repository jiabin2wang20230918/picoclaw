# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PicoClaw is an ultra-lightweight personal AI assistant written in Go. It runs on minimal hardware (<10MB RAM) and is designed to work with various LLM providers through a flexible configuration system.

## Architecture

The system follows a modular architecture with the following key components:

- **Agent Loop**: Main processing loop that handles messages and coordinates tool usage
- **Agent Instance**: Represents individual agents with their own workspace, session manager, and tool registry
- **Tool Registry**: Manages various tools (filesystem, exec, web, browser, cron, etc.)
- **Message Bus**: Handles inter-component communication
- **Config System**: Flexible configuration supporting multiple LLM providers and channels

## Key Files Structure

- `cmd/picoclaw/main.go`: Entry point with CLI command handlers
- `pkg/config/config.go`: Central configuration structure and loading
- `pkg/agent/loop.go`: Main agent processing loop
- `pkg/agent/instance.go`: Individual agent instance management
- `pkg/tools/*`: Various tools implementations (filesystem, web, exec, etc.)
- `pkg/channels/*`: Integration with various messaging platforms (Telegram, Discord, etc.)

## Development Commands

- `make deps`: Download dependencies
- `make build`: Build for current platform
- `make build-all`: Build for multiple platforms (linux-amd64, linux-arm64, etc.)
- `make install`: Install to system (~/.local/bin)
- `make test`: Run tests
- `make lint`: Run linters
- `make run ARGS="..."`: Build and run with arguments

## Configuration

The system uses a flexible configuration model:
- Default location: `~/.picoclaw/config.json`
- Supports multiple model providers via `model_list` configuration
- Environment variables override JSON config (uses caarlos0/env)
- Supports workspace restriction for security

## Key Configuration Options

- `model_list`: Defines available models with protocol prefixes (openai/, anthropic/, etc.)
- `agents.defaults`: Default agent settings including workspace, model, etc.
- `channels.*`: Configuration for various chat platforms (Telegram, Discord, etc.)
- `tools.*`: Configuration for various tools (web search, cron, exec restrictions)

## Security Features

- Workspace restriction limits file access to designated directories
- Command execution restrictions block dangerous patterns (rm -rf, shutdown, etc.)
- Tool sandboxing prevents unauthorized system access

## Testing

- Run tests: `make test`
- Individual packages can be tested separately with `go test`

## Building for Different Architectures

PicoClaw supports multiple architectures including x86_64, ARM64, RISC-V, and LoongArch64, making it suitable for deployment on resource-constrained devices like single-board computers.

## Git Commits

Commit messages should omit Claude Code attribution markers (like the
"🤖 Generated with Claude Code" footer).

Write meaningful commit messages that explain your changes:
- Start with a clear summary of what changed
- Focus on why the change was needed and how it addresses the problem
- Highlight any changes in behavior that users or other systems will observe
- Don't just describe what code lines were modified - that's already
  visible in the diff

When staging files for commits:
- Only include files that you created during this work session
- Leave pre-existing untracked files unstaged
- Don't commit changes to .gitignore unless explicitly needed
- Do not run git add or git commit unless the user explicitly asks
