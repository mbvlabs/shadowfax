<p align="center">
  <img width="450" height="350" alt="shadowfax-logo" src="https://github.com/user-attachments/assets/6f803a41-e6f7-447d-98c3-c10c727d6704" />
</p>

# Shadowfax

The development server and hot-reload runner for the [Andurel](https://github.com/mbvlabs/andurel) project.

## Features

- **Hot Reload** - Automatically rebuilds and restarts your Go application when source files change
- **Template Support** - Watches `.templ` files and triggers browser reloads when templates change (with TEMPL_DEV_MODE enabled)
- **Tailwind CSS** - Optional Tailwind CSS watcher that rebuilds and reloads on style changes
- **Reverse Proxy** - Proxies requests to your app server and injects the hot-reload script into HTML responses
- **Inertia SSR** - When `INERTIA_SSR_MODE=external`, runs the Node SSR server, builds the bundle when missing, and rebuilds on frontend source changes

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

### Inertia SSR

For Inertia projects, shadowfax always runs the Vite dev server (`npm run dev` or the configured package manager).

SSR settings follow the same precedence as generated `config/inertia.go`: process environment (including `.env`), then `.env.example`, then `DefaultInertiaSSR*` constants in `config/inertia.go`.

| Mode | Shadowfax | Go application |
|------|-----------|----------------|
| `disabled` | no SSR process | no SSR process |
| `external` | starts `node` on `INERTIA_SSR_BUNDLE`, watches `resources/js`, runs `build:ssr` on changes | connects to `INERTIA_SSR_URL` |
| `managed` | no SSR process | starts and stops SSR via Fx lifecycle |

Use `external` for local development so Go restarts do not fight shadowfax over the same Node process. Use `managed` when you want the application to own SSR (production-like).

Relevant variables: `INERTIA_SSR_URL` (default `http://127.0.0.1:13714`), `INERTIA_SSR_BUNDLE` (default `assets/dist/ssr/ssr.js`), `INERTIA_SSR_RUNTIME` (default `node`, or `andurel.lock` `scaffoldConfig.inertiaSSRRuntime`).

### Tailwind CSS

Tailwind watching is automatically enabled when the project has a `css/base.css` file.

## How It Works

1. **Go Watcher** - Monitors `.go` files (excluding `_templ.go`) and triggers a rebuild when changes are detected
2. **Templ Watcher** - Runs `templ generate --watch` to handle template changes
3. **Tailwind Watcher** - Runs the Tailwind CLI in watch mode (if enabled)
4. **Inertia SSR** - Runs the external Node SSR server when configured (if enabled)
5. **App Server** - Builds and runs `cmd/app/main.go`, restarting on rebuilds
6. **Proxy Server** - Intercepts HTML responses and injects a WebSocket client script
7. **Broadcaster** - Notifies all connected browsers to reload when changes are ready

## Project Structure

```
cmd/shadowfax/       # Entry point
internal/
  config/            # Configuration and lock file parsing
  proxy/             # Reverse proxy with script injection
  reload/            # Broadcaster, health checks, WebSocket handler
  server/            # App server lifecycle management
  watcher/           # File watchers (Go, templ, Tailwind, Inertia SSR sources)
  ssr/               # External Inertia SSR process management
```

## Contributing

Contributions are welcome! But please open an issue beforehand.

Here's how to get started:

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Make your changes and add tests
4. Run quality checks: `go vet ./...` and `golangci-lint run`
5. Commit your changes: `git commit -m 'Add amazing feature'`
6. Push to the branch: `git push origin feature/amazing-feature`
7. Open a Pull Request

## Acknowledgements

Shadowfax is based on these execellent open-source projects:

- **[Air](https://github.com/cosmtrek/air)** - Live reload for Go apps
- **[Templier](https://github.com/romshark/templier)** - A Go Templ web frontend development environment that automatically rebuilds the server and reloads the tab.

## License

MIT
