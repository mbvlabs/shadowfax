# Shadowfax

The development server and hot-reload runner for the [Andurel](https://github.com/mbvlabs/andurel) project.

## Features

- **Hot Reload** - Automatically rebuilds and restarts your Go application when source files change
- **Template Support** - Watches `.templ` files and triggers browser reloads when templates change (with TEMPL_DEV_MODE enabled)
- **Tailwind CSS** - Optional Tailwind CSS watcher that rebuilds and reloads on style changes
- **Reverse Proxy** - Proxies requests to your app server and injects the hot-reload script into HTML responses
- **Inertia** - When `--inertia` is passed (by `andurel run`), runs the Vite dev server. Development SSR is Vite's `/__inertia_ssr` endpoint; `cmd/ssr` is production-only.

## Installation

Download the latest binary from the [releases page](https://github.com/mbvlabs/shadowfax/releases) or install with Go:

```bash
go install github.com/mbvlabs/shadowfax/cmd/shadowfax@latest
```

Prefer `andurel run` in Andurel projects: it resolves project settings and invokes Shadowfax with the correct flags.

## Usage

```bash
# Typically invoked by andurel run, not by hand:
shadowfax \
  --inertia \
  --js-package-manager pnpm
```

Without `--inertia`, Shadowfax still runs the proxy, Go rebuild loop, and Templ watcher.

Open your browser to `http://localhost:3000` to see your app with hot-reload enabled.

## Configuration

### CLI flags (owned by `andurel run`)

| Flag | Default | Description |
|------|---------|-------------|
| `--inertia` | `false` | Enable the Vite dev server |
| `--js-package-manager` | `npm` | Package manager for `run dev` |
| `--ssr-url` | `http://127.0.0.1:13714` | Unused in development (kept for compatibility) |
| `--ssr-bundle` | `assets/dist/ssr/ssr.js` | Unused in development (kept for compatibility) |

Shadowfax does **not** parse `andurel.lock` or `config/inertia.go`. Pass `--inertia` and the package manager explicitly. Production `cmd/ssr` settings live in the app's `config/inertia.go`.

### Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `PROXY_PORT` | `3000` | Port for the proxy server (use this in your browser) |
| `PORT` | `8080` | Port for the app server (internal) |
| `SHADOWFAX_VERBOSE` | `false` | Enable verbose debug logging |

### Inertia SSR ownership

Under `andurel run`, Shadowfax starts Vite. In development, `cmd/app` posts SSR
requests to Vite's `/__inertia_ssr` endpoint (HMR, no `cmd/ssr` rebuild).
Production still uses `cmd/ssr` and `vite build --ssr`. Pages opt in with
`inertia.WithSSR()` / `.SSR()`.

### Tailwind CSS

Tailwind CLI watching is enabled when the project has a `css/base.css` file and
`--inertia` is not set. Inertia apps use `@tailwindcss/vite` instead.

## How It Works

1. **Go Watcher** - Monitors `.go` files (excluding `_templ.go`) and triggers a rebuild when changes are detected
2. **Templ Watcher** - Runs `templ generate --watch` to handle template changes
3. **Tailwind Watcher** - Runs the Tailwind CLI in watch mode (Templ projects)
4. **Inertia** - With `--inertia`, runs Vite; CSS and JS HMR stay on Vite
5. **App Server** - Builds and runs `cmd/app/main.go`, restarting on rebuilds
6. **Proxy Server** - Intercepts HTML responses and injects a WebSocket client script
7. **Broadcaster** - Notifies all connected browsers to reload when changes are ready

## Project Structure

```
cmd/shadowfax/       # Entry point
internal/
  config/            # CLI run options and helpers
  proxy/             # Reverse proxy with script injection
  reload/            # Broadcaster, health checks, WebSocket handler
  server/            # App server lifecycle management
  watcher/           # File watchers (Go, templ, Tailwind, Inertia SSR sources)
  ssr/               # Builds and supervises cmd/ssr in development
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
