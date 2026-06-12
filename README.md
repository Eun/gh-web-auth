# gh-web-auth

A lightweight Go daemon that performs GitHub OAuth device-flow authentication via a web UI, writing tokens in the same format as the official `gh` CLI.

## How it works

This application reuses the **exact same OAuth flow and credentials** as the official [GitHub CLI](https://github.com/cli/cli):

- **OAuth Client ID**: `178c6fc778ccc68e1d6a` (the "GitHub CLI" OAuth app)
- **OAuth Client Secret**: `34ddeff2b558a23d38fba8a6de74f086ede1cc0b`
- **OAuth library**: [`github.com/cli/oauth`](https://github.com/cli/oauth) v1.2.2 — the same device-flow library `gh` uses internally
- **Token storage**: Writes to `~/.config/gh/hosts.yml` in the exact same YAML format that `gh` expects, so all `gh` commands work seamlessly after auth

### Flow

1. The daemon starts an HTTP server on port **8080**
2. On page load, the UI calls `/api/status` to validate any stored token against the GitHub API (`X-Oauth-Scopes` header check)
3. If not authenticated (or token expired), clicking **Login with GitHub** triggers:
   - A `POST /login/device/code` request to GitHub with the CLI's OAuth client ID and scopes (`repo`, `read:org`, `gist`, `workflow`)
   - The device code and verification URL are returned to the browser
   - The UI displays the one-time code prominently and opens `github.com/login/device` in a new tab
   - The server polls `POST /login/oauth/access_token` (via `device.Wait`) until the user completes auth
4. On successful auth, the server:
   - Queries the GraphQL API (`viewer { login }`) to resolve the username
   - Writes the token to `~/.config/gh/hosts.yml`
5. On revisit, the stored token is validated against the GitHub REST API
6. **Re-authenticate** kills any active flow, removes the stored token, and starts fresh

## No `gh` binary required

Unlike the previous version, this application does **not** shell out to the `gh` CLI. It performs the full OAuth device flow natively using the `cli/oauth` library and writes the config file directly. The `gh` binary is only needed downstream by the user for their actual GitHub operations.

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
mise run run
```

Open http://localhost:8080

## Tasks

```bash
mise run build      # Build the binary
mise run run        # Build and run
mise run install    # Install as systemd service
mise run uninstall  # Remove systemd service and binary
mise run clean      # Remove build artifacts
```

## Pinned tools

Tool versions are pinned in `mise.toml`. Run `mise install` to set up the environment.

## Dependencies

- Go 1.22+ (build-time only)
- `github.com/cli/oauth` v1.2.2 — GitHub OAuth device/web-app flow
- `gopkg.in/yaml.v3` — YAML serialization for gh config

## Project structure

```
gh-web-auth/
├── main.go          # HTTP server, session management, API handlers
├── oauth.go         # OAuth client ID/secret, scopes, host constants
├── github.go        # GitHub API: token validation + GraphQL viewer lookup
├── ghconfig.go      # Read/write ~/.config/gh/hosts.yml
├── static/
│   └── index.html   # Single-page web UI
├── go.mod
├── go.sum
├── gh-web-auth.service  # systemd unit
├── mise.toml            # pinned tool versions + tasks
└── README.md
```
