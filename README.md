<p align="center">
  <img width="1024" height="1536" alt="shadowfax-logo" src="https://github.com/user-attachments/assets/6f803a41-e6f7-447d-98c3-c10c727d6704" />
</p>

# Shadowfax

The development server and hot-reload runner for the [Andurel](https://github.com/mbvlabs/andurel) project.

## Features

- **Hot Reload** - Automatically rebuilds and restarts your Go application when source files change
- **Template Support** - Watches `.templ` files and triggers browser reloads when templates change (with TEMPL_DEV_MODE enabled)
- **Tailwind CSS** - Optional Tailwind CSS watcher that rebuilds and reloads on style changes
- **Reverse Proxy** - Proxies requests to your app server and injects the hot-reload script into HTML responses
- **WebSocket-based** - Uses WebSockets for instant browser refresh notifications

## Installation

Download the latest binary from the [releases page](https://github.com/mbvlabs/shadowfax/releases) or install with Go:

```bash
go install github.com/mbvlabs/shadowfax/cmd/shadowfax@latest
```

## Usage

Run `shadowfax` in your Andurel project directory:

```bash
shadowfax
```

This will:
1. Start a proxy server on port 3000 (configurable via `PROXY_PORT`)
2. Build and run your app on port 8080 (configurable via `PORT`)
3. Watch for file changes and automatically rebuild/reload

Open your browser to `http://localhost:3000` to see your app with hot-reload enabled.

## Configuration

Shadowfax is configured via environment variables (supports `.env` files):

| Variable | Default | Description |
|----------|---------|-------------|
| `PROXY_PORT` | `3000` | Port for the proxy server (use this in your browser) |
| `PORT` | `8080` | Port for the app server (internal) |
| `SHADOWFAX_VERBOSE` | `false` | Enable verbose debug logging |

### Tailwind CSS

Tailwind watching is automatically enabled if your `andurel.lock` file specifies Tailwind as the CSS framework:

```json
{
  "scaffoldConfig": {
    "cssFramework": "tailwind"
  }
}
```

## How It Works

1. **Go Watcher** - Monitors `.go` files (excluding `_templ.go`) and triggers a rebuild when changes are detected
2. **Templ Watcher** - Runs `templ generate --watch` to handle template changes
3. **Tailwind Watcher** - Runs the Tailwind CLI in watch mode (if enabled)
4. **App Server** - Builds and runs `cmd/app/main.go`, restarting on rebuilds
5. **Proxy Server** - Intercepts HTML responses and injects a WebSocket client script
6. **Broadcaster** - Notifies all connected browsers to reload when changes are ready

## Project Structure

```
cmd/shadowfax/       # Entry point
internal/
  config/            # Configuration and lock file parsing
  proxy/             # Reverse proxy with script injection
  reload/            # Broadcaster, health checks, WebSocket handler
  server/            # App server lifecycle management
  watcher/           # File watchers (Go, templ, Tailwind)
```

## License

MIT
