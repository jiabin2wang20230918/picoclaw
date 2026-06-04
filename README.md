# QuantClaw

QuantClaw is a lightweight Go agent runtime for running a personal AI assistant from the terminal, as a long-running gateway, or behind chat channels.

It is designed for practical automation: one binary, JSON configuration, pluggable LLM providers, tool execution, workspace memory, scheduled tasks, and chat integrations.

## What It Does

- Runs one-shot or interactive assistant sessions from the CLI.
- Connects to multiple model providers through `model_list`.
- Executes tools for files, shell commands, web search, skills, cron jobs, and subagents.
- Maintains workspace state, sessions, memory files, and scheduled tasks.
- Serves chat gateways for Telegram, Discord, Slack, DingTalk, Feishu, LINE, QQ, OneBot, WeCom, WhatsApp bridges, and MaixCam.
- Supports heartbeat tasks for periodic background automation.
- Provides optional workspace restrictions for safer file and command access.
- Builds as a small self-contained Go binary for Linux, macOS, and Windows targets.

## Quick Start

### Build From Source

```bash
git clone https://github.com/sipeed/quantclaw.git
cd quantclaw
make build
```

The binary is written to `build/quantclaw-<platform>-<arch>` and symlinked as `build/quantclaw`.

### Initialize

```bash
./build/quantclaw onboard
```

This creates:

```text
~/.quantclaw/config.json
~/.quantclaw/workspace/
```

### Configure A Model

Edit `~/.quantclaw/config.json`:

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.quantclaw/workspace",
      "restrict_to_workspace": true,
      "model": "gpt4",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "model_list": [
    {
      "model_name": "gpt4",
      "model": "openai/gpt-5.2",
      "api_key": "sk-your-openai-key",
      "api_base": "https://api.openai.com/v1"
    }
  ]
}
```

Then run:

```bash
./build/quantclaw agent -m "Summarize this repository"
```

For a complete template, see [config/config.example.json](config/config.example.json).

## CLI

| Command | Description |
| --- | --- |
| `quantclaw onboard` | Create config and workspace files |
| `quantclaw agent -m "..."` | Run one assistant request |
| `quantclaw agent` | Start an interactive terminal session |
| `quantclaw gateway` | Start configured chat channels and background services |
| `quantclaw status` | Show local status |
| `quantclaw auth ...` | Manage OAuth/token-based provider authentication |
| `quantclaw cron ...` | Manage scheduled jobs |
| `quantclaw skills ...` | Install, list, search, and manage skills |
| `quantclaw migrate ...` | Migrate data from OpenClaw-style layouts |
| `quantclaw version` | Print build version |

## Docker

```bash
cp config/config.example.json config/config.json
vim config/config.json
docker compose --profile gateway up -d
```

Run a one-shot agent request:

```bash
docker compose run --rm quantclaw-agent -m "What changed in this repo?"
```

By default, the gateway binds to local interfaces from the config. If you need container ports exposed to the host, set the gateway host to `0.0.0.0` in config or with environment variables such as `QUANTCLAW_GATEWAY_HOST=0.0.0.0`, then add the required `ports` mapping in `docker-compose.yml`.

## Configuration

QuantClaw uses a single JSON config file:

```text
~/.quantclaw/config.json
```

The most important sections are:

| Section | Purpose |
| --- | --- |
| `agents.defaults` | Default model, workspace, token limits, sandbox setting |
| `model_list` | Model provider definitions and aliases |
| `channels` | Chat channel credentials and webhook settings |
| `tools` | Web search, browser, cron, shell, and skill settings |
| `heartbeat` | Periodic background task settings |
| `providers` | Legacy provider config, kept for compatibility |

### Workspace

Default workspace:

```text
~/.quantclaw/workspace/
├── sessions/
├── memory/
├── state/
├── cron/
├── skills/
├── context.json
├── AGENT.md
├── HEARTBEAT.md
├── IDENTITY.md
├── SOUL.md
└── USER.md
```

The workspace is where QuantClaw keeps conversation state, memory files, scheduled task data, and user-provided skills.

## Model Providers

QuantClaw routes requests by protocol family. New OpenAI-compatible endpoints can usually be added by setting `model`, `api_base`, and `api_key` in `model_list`.

Common model prefixes:

| Prefix | Backend |
| --- | --- |
| `openai/` | OpenAI API |
| `anthropic/` | Anthropic Claude API |
| `openrouter/` | OpenRouter |
| `zhipu/` | Zhipu GLM |
| `deepseek/` | DeepSeek |
| `gemini/` | Gemini-compatible endpoint |
| `groq/` | Groq OpenAI-compatible API |
| `qwen/` | DashScope Qwen compatible mode |
| `ollama/` | Local Ollama |
| `vllm/` | Local or remote vLLM-compatible server |
| `cerebras/` | Cerebras API |
| `antigravity/` | Google Antigravity OAuth flow |
| `github-copilot/` | GitHub Copilot provider |

Example with a local Ollama model:

```json
{
  "model_list": [
    {
      "model_name": "local-llama",
      "model": "ollama/llama3"
    }
  ],
  "agents": {
    "defaults": {
      "model": "local-llama"
    }
  }
}
```

Example with load balancing:

```json
{
  "model_list": [
    {
      "model_name": "gpt4",
      "model": "openai/gpt-5.2",
      "api_base": "https://api1.example.com/v1",
      "api_key": "sk-key1"
    },
    {
      "model_name": "gpt4",
      "model": "openai/gpt-5.2",
      "api_base": "https://api2.example.com/v1",
      "api_key": "sk-key2"
    }
  ]
}
```

## Tools

QuantClaw includes tools for:

- Reading, writing, listing, editing, and appending files.
- Running shell commands with safety guards.
- Web search and web fetch.
- Browser automation through Chrome DevTools Protocol.
- Cron-backed reminders and scheduled jobs.
- Installing and running skills.
- Spawning subagents for long-running tasks.
- Sending progress or final messages back through channels.

Tool behavior is configured under `tools` in `config.json`.

## Chat Gateways

Start all enabled channels:

```bash
quantclaw gateway
```

Supported channel configs include:

| Channel | Typical Credentials |
| --- | --- |
| Telegram | Bot token and allowed user IDs |
| Discord | Bot token, intents, allow list |
| Slack | Bot token and app token |
| DingTalk | Client ID and client secret |
| Feishu | App ID, app secret, verification token |
| LINE | Channel secret, channel access token, webhook |
| QQ | App ID and app secret |
| OneBot | WebSocket URL and access token |
| WeCom | Bot webhook or app credentials |
| WhatsApp | Bridge WebSocket URL |
| MaixCam | Host and port |

See the channel-specific docs under [docs/channels](docs/channels).

## Scheduled And Background Work

QuantClaw supports two related mechanisms:

- `cron`: explicit reminders and recurring jobs created by the agent or CLI.
- `heartbeat`: periodic workspace prompts from `HEARTBEAT.md`.

Example `HEARTBEAT.md`:

```markdown
# Periodic Tasks

- Check whether any scheduled jobs are due.
- Summarize important updates from my workspace.
- Use spawn for long-running research tasks.
```

Enable or tune heartbeat:

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

Environment overrides:

```bash
export QUANTCLAW_HEARTBEAT_ENABLED=false
export QUANTCLAW_HEARTBEAT_INTERVAL=60
```

## Security Boundary

Enable workspace restriction:

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.quantclaw/workspace",
      "restrict_to_workspace": true
    }
  }
}
```

When enabled, file tools and command execution are restricted to the configured workspace. The shell tool also blocks dangerous command patterns such as bulk deletion, disk formatting, direct disk writes, shutdown commands, and fork bombs.

This is a guardrail, not a complete sandbox. Run QuantClaw with least-privilege OS users and avoid putting secrets in files the agent can access unless the workflow requires them.

## Build Targets

```bash
make build       # current platform
make build-acp   # ACP protocol binary
make build-all   # common release targets
make test        # Go tests
make install     # install to ~/.local/bin by default
```

Generated binaries:

```text
build/quantclaw
build/quantclaw-acp
```

## Development Notes

- Go module: `github.com/sipeed/quantclaw`
- Main command: [cmd/quantclaw](cmd/quantclaw)
- ACP command: [cmd/quantclaw-acp](cmd/quantclaw-acp)
- Default config template: [config/config.example.json](config/config.example.json)
- Architecture notes: [docs/modular-architecture-enhancements.md](docs/modular-architecture-enhancements.md)
- Migration notes: [docs/migration/model-list-migration.md](docs/migration/model-list-migration.md)

## Troubleshooting

### Web Search Returns Configuration Errors

Web search works best with a configured provider such as Brave or Tavily. DuckDuckGo fallback can work without an API key but may be less reliable.

```json
{
  "tools": {
    "web": {
      "brave": {
        "enabled": true,
        "api_key": "YOUR_BRAVE_API_KEY",
        "max_results": 5
      },
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      }
    }
  }
}
```

### Telegram Reports `Conflict: terminated by other getUpdates`

Only one `quantclaw gateway` process should run against a Telegram bot token at a time.

### Provider Content Filtering

Some providers apply content filters. Try a different model or rephrase the request.

## License

MIT. See [LICENSE](LICENSE).
