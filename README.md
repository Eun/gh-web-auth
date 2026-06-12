# gh-web-auth

A lightweight Go daemon that performs GitHub OAuth device-flow authentication via a web UI, writing tokens in the same format as the official `gh` CLI.

## How it works

This application reuses the **same OAuth flow** as the official [GitHub CLI](https://github.com/cli/cli), with all OAuth settings fully configurable via CLI flags or environment variables:

- **OAuth Client ID / Secret**: Defaults to the "GitHub CLI" OAuth app credentials, but can be overridden for GitHub Enterprise Server or custom OAuth apps
- **OAuth library**: [`github.com/cli/oauth`](https://github.com/cli/oauth) v1.2.2 — the same device-flow library `gh` uses internally
- **Token storage**: Writes to `~/.config/gh/hosts.yml` (configurable) in the exact same YAML format that `gh` expects, so all `gh` commands work seamlessly after auth

### Flow

1. The daemon starts an HTTP server on the configured listen address (default **0.0.0.0:8080**)
2. On page load, the UI calls `/api/status` to validate any stored token against the GitHub API (`X-Oauth-Scopes` header check)
3. If not authenticated (or token expired), clicking **Login with GitHub** triggers:
   - A `POST /login/device/code` request to the configured GitHub host with the OAuth client ID and scopes
   - The device code and verification URL are returned to the browser
   - The UI displays the one-time code prominently and opens the device verification page in a new tab
   - The server polls `POST /login/oauth/access_token` (via `device.Wait`) until the user completes auth
4. On successful auth, the server:
   - Queries the GraphQL API (`viewer { login }`) to resolve the username
   - Writes the token to the gh config directory
5. On revisit, the stored token is validated against the GitHub REST API
6. **Re-authenticate** kills any active flow, removes the stored token, and starts fresh

## Configuration

All settings can be provided as CLI flags or environment variables. Flags take precedence over environment variables.

| Flag | Env Var | Default | Description |
|---|---|---|---|
| `--listen-addr` | `GH_WEB_AUTH_LISTEN_ADDR` | `0.0.0.0:8080` | Address to listen on |
| `--gh-config-dir` | `GH_CONFIG_DIR` | `~/.config/gh` | gh config directory |
| `--gh-host` | `GH_HOST` | `github.com` | GitHub hostname (supports GHES) |
| `--gh-client-id` | `GH_CLIENT_ID` | `178c6fc778ccc68e1d6a` | OAuth client ID |
| `--gh-client-secret` | `GH_CLIENT_SECRET` | `34ddeff2b558a23d38fba8a6de74f086ede1cc0b` | OAuth client secret |
| `--gh-scopes` | `GH_SCOPES` | `repo,read:org,gist,workflow` | Comma-separated OAuth scopes |
| `--gh-git-protocol` | `GH_GIT_PROTOCOL` | _(empty)_ | Git protocol (https or ssh) |

### GHES example

```bash
gh-web-auth --gh-host ghes.company.com --gh-client-id YOUR_ID --gh-client-secret YOUR_SECRET
```

### Environment variable example

```bash
export GH_WEB_AUTH_LISTEN_ADDR=0.0.0.0:3000
export GH_HOST=ghes.company.com
gh-web-auth
```

## Installation

### Docker

```bash
docker run -p 8080:8080 ghcr.io/eun/gh-web-auth:latest
```

Multi-arch images are published for `linux/amd64` and `linux/arm64`.

### Debian / Ubuntu

Download the `.deb` from the [latest release](https://github.com/Eun/gh-web-auth/releases) and install:

```bash
dpkg -i gh-web-auth_*.deb
```

Packages are available for `amd64` and `arm64`.

### Binary

Download the appropriate binary from the [releases page](https://github.com/Eun/gh-web-auth/releases).

## No `gh` binary required

This application does **not** shell out to the `gh` CLI. It performs the full OAuth device flow natively using the `cli/oauth` library and writes the config file directly. The `gh` binary is only needed downstream by the user for their actual GitHub operations.

## Endpoints

| Method | Path              | Description                                          |
|--------|-------------------|------------------------------------------------------|
| GET    | `/`               | Web UI                                               |
| GET    | `/api/status`     | Validate stored token, return user + scopes          |
| POST   | `/api/login`      | Start device flow, return `{code, url}`              |
| GET    | `/api/login/poll` | Poll login completion → `{done, success, error}`     |
| POST   | `/api/logout`     | Remove stored token                                  |
| POST   | `/api/reauth`     | Force re-authentication (logout + new device flow)   |

## Quick start

```bash
mise install   # install Go, golangci-lint, goreleaser
mise run build
./gh-web-auth
```

Open http://localhost:8080

## Releases

Releases are managed via [GoReleaser](https://goreleaser.com/) and GitHub Actions. When a release is published on GitHub:

- Binaries are built for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`
- `.deb` packages are produced for `amd64` and `arm64`
- Docker images are pushed to `ghcr.io/eun/gh-web-auth` with multi-arch manifests (`amd64` + `arm64`)
- Tags: `:v1.2.3`, `:1`, `:1.2`, `:latest`

## Dependencies

- Go 1.22+ (pinned via mise.toml) (build-time only)
- `github.com/cli/oauth` v1.2.2 — GitHub OAuth device/web-app flow
- `github.com/urfave/cli/v2` — CLI flag and environment variable parsing
- `gopkg.in/yaml.v3` — YAML serialization for gh config

## Project structure

```
gh-web-auth/
├── main.go                              # HTTP server, session management, API handlers
├── oauth.go                             # Default OAuth constants
├── github.go                            # GitHub API: token validation + GraphQL viewer lookup
├── ghconfig.go                          # Read/write ~/.config/gh/hosts.yml
├── static/
│   └── index.html                       # Single-page web UI
├── mise.toml                            # Tool versions + tasks (build, lint, release, clean)
├── .goreleaser.yml                      # GoReleaser config (multi-arch Docker + deb)
├── .golangci.yml                        # Linter configuration
├── .github/
│   ├── Dockerfile                       # Distroless container image
│   ├── PULL_REQUEST_TEMPLATE.md
│   ├── dependabot.yml
│   ├── labeler.yml
│   ├── release-drafter.yml
│   └── workflows/
│       ├── push.yml                     # Lint, vuln scan, build, release draft
│       ├── release_published.yml        # GoReleaser release
│       └── pull_request_target_opened.yml
├── go.mod
├── go.sum
└── README.md
```
