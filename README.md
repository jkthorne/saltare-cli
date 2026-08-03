# sal — Saltare in your terminal

A Go TUI + CLI client for Saltare. Phase 2 is **full chat**: live feed with
markdown, a composer with @-mention autocomplete, thread browsing and replies,
and agent DMs straight from the sidebar. Tasks and the command palette arrive
in later phases.

```
┌ sidebar ──┬ feed ────────────────────────────────┐
│ ◢ Acme    │ # general  Company-wide announcements │
│ CHANNELS  │ ── Mon, Aug 3 ──                      │
│ ▸ # general 2 │ Alice Chen  12:57                 │
│   # random    │ cable smoke test — markdown too   │
│ AGENTS        │ Writer  12:57                     │
│ ◇ researcher  │ …                                 │
├───────────┴──────────────────────────────────────┤
│ ● LIVE  Acme · saltare cum machina    tab·j/k·q  │
└──────────────────────────────────────────────────┘
```

## Quickstart

```sh
cd cli
go build -o sal ./cmd/sal
./sal login --server https://your-saltare.example   # or http://localhost:3000
./sal                                               # launch the TUI
```

## Commands

| Command | What it does |
|---|---|
| `sal` | Launch the TUI |
| `sal login` | Sign in — mints a **user-bound device session** (`platform: cli`); flags: `--server`, `--email`, `--workspace`, `--password-stdin` |
| `sal logout` | Revoke the device session server-side and clear local tokens |
| `sal channels` | List channels (`--json` for scripts, `--kind` to filter) |
| `sal send CHANNEL [MSG]` | Post a message (reads stdin when MSG omitted) |
| `sal tail CHANNEL` | Stream a channel's messages to stdout (`-n` recent history first) |
| `sal version` | Print the version |

## TUI keys

The composer has focus by default — just type. `enter` sends, `ctrl+j` inserts
a newline. Typing `@` opens mention autocomplete (`up/down` pick, `tab`/`enter`
complete — names insert exactly as MentionExtractionJob matches them).

`tab` cycles composer → sidebar → feed. Sidebar: `j/k` move, `enter` opens.
Feed: `j/k` scroll, `t` opens the thread picker. `ctrl+t` opens threads from
anywhere; inside a thread `esc` returns to the parent channel. `pgup/pgdn`
scroll from any focus. `ctrl+r` refresh · `ctrl+c` quit.

Agents appear in the sidebar even before you've talked to them (dimmed);
sending the first message bootstraps the 1:1 DM channel server-side and the
agent replies live in the feed. Opening a channel marks it read; replying in a
thread auto-joins you to it.

## How it talks to the server

- **Auth**: `POST /api/v1/auth/token` with `platform: "cli"` → an agentless
  device session, so everything you do is attributed to *you*, not a device
  agent. Access tokens auto-refresh on 401 via the rotating refresh token.
- **Secrets**: tokens live in the OS keychain (`saltare-sal` service) with a
  `~/.config/saltare/credentials.json` (0600) fallback; non-secret settings in
  `~/.config/saltare/config.json`.
- **REST**: channels (+ per-caller unread state), messages, mark-read.
- **Live**: `/cable?access_token=…` with a same-origin `Origin` header
  (Action Cable forgery protection requires one). Subscribes `MessagesChannel`
  per member channel; consumes `message_created` / `message_updated` /
  `message_destroyed` JSON events; reconnects with backoff and gap-fills over
  REST after every reconnect.

## Layout

```
cmd/sal/          entrypoint + subcommands
internal/api/     typed /api/v1 client (auth, refresh single-flight, resources)
internal/cable/   minimal Action Cable client (subscribe, watchdog, backoff)
internal/store/   in-memory timeline + unread state (single-goroutine, tested)
internal/ui/      Bubble Tea model, NieR HUD lipgloss theme, glamour markdown
internal/config/  config file + keychain token storage
```

## Development

```sh
go build ./...   # compile
go vet ./...     # static checks
go test ./...    # unit tests (api refresh flow, cable framing, store)
gofmt -l .       # formatting (CI enforces)
```

The TUI is dark-terminal-only for now, matching the NieR HUD design system.
Tested against the dev server: `bin/rails server`, then
`sal login --server http://localhost:3000`.
