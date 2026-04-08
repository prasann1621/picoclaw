# Picoclaw

AutoAgent-inspired autonomous agent in Go with Telegram integration, daily learning, and skill tracking.

## Features

- **AutoAgent-Inspired**: Reverse-engineered from AutoAgent Python codebase
- **Telegram Bot**: Full command interface with `/skill`, `/build`, `/report`, `/weekly`
- **Daily Learning**: Automatic skill learning with progress tracking
- **Tool Building**: Automatically builds and publishes tools to GitHub
- **Weekly Reports**: Detailed weekly learning summaries
- **Multi-Provider**: Supports Google Gemini, OpenAI, Anthropic, OpenRouter, NVIDIA

## Commands

```
/start, /help   - Show help
/status         - Agent status
/tools          - List tools
/skill <name>   - Learn a new skill
/skills         - List learned skills
/build <name>   - Build a tool
/report         - Get daily report
/weekly         - Get weekly report
/search <query> - Web search
/time           - Current time
/config         - Show config
/models         - List available models
/set_model      - Switch model
/restart        - Restart agent
```

## Build

```bash
go build -o picoclaw .
```

## Run

```bash
./picoclaw
```

## Configuration

Edit `/root/.picoclaw/config.json` to configure:
- Telegram bot token
- Learning settings
- API keys

## Learning System

Picoclaw automatically:
1. Learns new skills daily
2. Tracks files modified
3. Records improvements
4. Builds tools
5. Sends daily/weekly reports to Telegram