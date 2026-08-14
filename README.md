# coolify-tui

A terminal dashboard for [Coolify](https://coolify.io) — monitor your applications
across every server, check their status, trigger deployments and watch them run,
without leaving the terminal. Think `lazydocker`, for Coolify.

> **Status:** in active development, built in phases. See [Roadmap](#roadmap).

## Why

Coolify's web dashboard is good, but if you live in a terminal, checking whether
`api` is healthy on `prod-2` shouldn't need a browser tab. `coolify-tui` gives
you one keyboard-driven view over every server and application a Coolify
instance manages.

## Features

- **Multi-instance** — flip between separate Coolify installs (prod, homelab, …)
- **Server-grouped inventory** — applications listed under the server they run on
- **Status at a glance** — `running:healthy`, degraded and stopped, colour-coded
- **Deploy** — trigger a deployment, or a force rebuild without the layer cache
- **Lifecycle** — start, stop and restart applications, with confirmation
- **Deployment history and live build logs** — follow a running build
- **Live container logs** — tail a running application
- **Environment variables** — read-only, masked by default
- **Server health** — reachability, usability and proxy state

## Install

Requires Go 1.24 or newer.

```sh
go install github.com/itsnitinr/coolify-tui@latest
```

Or build from source:

```sh
git clone https://github.com/itsnitinr/coolify-tui
cd coolify-tui
go build -o coolify-tui .
```

## Getting a Coolify API token

1. In Coolify, go to **Security → API Tokens**
2. Give the token a name and an expiry
3. Select these permissions:

   | Permission       | Needed for                                   |
   | ---------------- | -------------------------------------------- |
   | `read`           | listing servers, applications, deployments   |
   | `read:sensitive` | container logs and environment variables     |
   | `write`          | start / stop / restart                       |
   | `deploy`         | triggering deployments                       |

   `read` alone is enough for a read-only monitoring setup.

4. Copy the token — Coolify shows it exactly once. It looks like `42|abc123…`.

Also note your instance URL, e.g. `https://coolify.example.com` (a bare host or
an IP with a port both work).

## Configuration

The config file lives at:

```
${XDG_CONFIG_HOME:-~/.config}/coolify-tui/config.yaml
```

It is created with mode `0600` inside a `0700` directory. Print the path with
`coolify-tui -config`.

```yaml
active_instance: prod
refresh_interval: 10s
log_lines: 500
confirm_destructive: true

instances:
  - name: prod
    url: https://coolify.example.com
    token: "42|your-token-here"

  - name: homelab
    url: http://192.168.1.10:8000
    # Keep the token out of the file: read it from the environment instead.
    token_env: HOMELAB_COOLIFY_TOKEN
    # For an instance behind a self-signed certificate:
    insecure_skip_verify: true
```

### Keeping tokens out of the file

Set `token_env` to the name of an environment variable instead of writing
`token`. This is the recommended setup on a shared machine or when your dotfiles
are in git:

```sh
export HOMELAB_COOLIFY_TOKEN="42|your-token-here"
```

`token_env` takes precedence over an inline `token` if both are present.

## Usage

```sh
coolify-tui                    # launch the dashboard
coolify-tui -instance homelab  # open a specific instance
coolify-tui doctor             # check every instance is reachable and authorised
coolify-tui instances          # list configured instances (no tokens shown)
coolify-tui -config            # print the config file path
coolify-tui -version
```

`doctor` is the first thing to run after setup. It verifies connectivity, prints
the Coolify version, and tells you which permission a token is missing:

```
prod (https://coolify.example.com)
  base URL: https://coolify.example.com/api/v1
  ✓ reachable, Coolify 4.0.0-beta.397
  ✓ read (servers): 3 servers (prod-2=unreachable)
  ✓ read (applications): 12 applications
  ✓ read (deployments): 1 queued/running
```

## Security

- The config file is written `0600` in a `0700` directory, atomically.
- `coolify-tui` warns at startup if the config file is more permissive than
  `0600`.
- Tokens are never written to logs or error messages; anything displayed goes
  through redaction that masks the secret half of the token.
- Environment variable values are masked in the UI until explicitly revealed.
- `.gitignore` excludes `config.yaml` and `.env*` so a stray copy inside a clone
  cannot be committed.
- `insecure_skip_verify` is opt-in per instance and disables TLS verification —
  only use it for a self-signed homelab instance.

## Roadmap

- [x] **Phase 1** — API client, config, `doctor`
- [ ] **Phase 2** — onboarding wizard
- [ ] **Phase 3** — dashboard shell
- [ ] **Phase 4** — deploy and lifecycle actions
- [ ] **Phase 5** — deployment history and live build logs
- [ ] **Phase 6** — container logs, environment variables, server health
- [ ] **Phase 7** — search, help overlay, release tooling

## Development

```sh
go test ./...      # unit tests, no network required
go vet ./...
gofmt -l .
```

The Coolify client is tested against `httptest` servers replaying recorded
response shapes, so the suite needs no live instance and no credentials.

## Credits

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles) and
[Lipgloss](https://github.com/charmbracelet/lipgloss).

Not affiliated with the Coolify project.

## License

[MIT](LICENSE)
