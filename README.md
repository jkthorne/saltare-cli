# sal — Saltare in your terminal

A Go TUI + CLI client for Saltare: live chat with markdown and @-mentions,
threads (browse, reply, or start one from any message), agent DMs, a tasks
pane, a command palette, a notifications inbox — and a built-in **Claude
assistant** riding the workspace's metered inference proxy.

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

## Install

Homebrew (needs repo access — the tap builds from source over SSH because
this repo is private):

```sh
brew tap jkthorne/tap git@github.com:jkthorne/homebrew-tap.git
brew install sal
```

Or grab a `sal_*_<os>_<arch>.tar.gz` from a GitHub release. Releases are cut
by pushing a `v*` tag (`.github/workflows/release.yml` runs goreleaser;
config in `cli/.goreleaser.yaml`). After tagging, bump `tag`/`version` in the
tap's `Formula/sal.rb`. When this repo goes public, switch the tap to
goreleaser's generated binary formulas (block ready in `.goreleaser.yaml`).

From source:

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
| `sal tasks` | List your open tasks (`--all`, `--state S`, `--json`) |
| `sal tasks complete SLUG` | Mark a task completed |
| `sal tasks add TITLE` | Create a self-assigned task (`--project SLUG`) |
| `sal ask QUESTION` | One-shot Claude answer streamed to stdout (`--model`; reads stdin when QUESTION omitted) |
| `sal version` | Print the version |

## TUI keys

The composer has focus by default — just type. `enter` sends, `ctrl+j` inserts
a newline. Typing `@` opens mention autocomplete (`up/down` pick, `tab`/`enter`
complete — names insert exactly as MentionExtractionJob matches them).

`tab` cycles composer → sidebar → feed. Sidebar: `j/k` move, `enter` opens.
Feed focus is **selection mode**: `j/k` moves a message cursor (arc gutter
bar); `t` opens the selected message's thread, or arms *reply-in-new-thread*
if it has none — your reply creates the thread and the view follows it.
`ctrl+t` opens the thread picker; inside a thread `esc` returns to the parent.
`pgup/pgdn` scroll from any focus. `ctrl+r` refresh · `ctrl+c` quit.

`ctrl+k` opens the **command palette**: fuzzy-jump to any channel or agent, or
run actions (assistant, tasks views, new task, notifications). `ctrl+n` opens
the **notifications inbox** — `enter` jumps to the channel, `R` marks all read.

`ctrl+g` toggles the **assistant** — a local Claude session over the
workspace's inference proxy (the server holds the provider key and meters
credits; the conversation itself never leaves your terminal). Streaming, with
markdown rendering and token usage per answer. No tool access yet. In the
feed, `o` loads older history without losing your scroll position, and
`[[type:slug]]` embeds render as `⟨type:slug⟩` chips.

The **tasks pane** (via palette): `j/k` move, `x`/`enter` complete or reopen,
`n` new task (title, then a project pick when several exist — tasks created
here are self-assigned), `m` toggles mine/all, `esc` back to chat. Overdue
tasks glow phoenix-red.

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

**tmux + vim-tmux-navigator users**: that config binds `C-h/j/k/l` globally
and only forwards them to whitelisted programs — so sal's `ctrl+k` (palette)
and `ctrl+j` (newline) silently become pane navigation. Add `sal` to the
`is_vim` process regex the same way `fzf` is usually whitelisted:

```
| grep -iqE '^[^TXZ ]+ +(\\S+\\/)?g?(view|l?n?vim?x?|fzf|sal)(diff)?$'
```

The TUI is dark-terminal-only for now, matching the NieR HUD design system.
Tested against the dev server: `bin/rails server`, then
`sal login --server http://localhost:3000`.
