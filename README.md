# sal — Saltare in your terminal

A Go TUI + CLI client for Saltare: a boot **home dashboard** (unread
channels + your due work), live chat with markdown and @-mentions,
threads (browse, reply, or start one from any message), agent DMs, message
editing, workspace search, **documents read and edited in your $EDITOR**,
tasks with detail views, discussions, and a day-grouped **agenda**,
composer file **attach**, follow-the-`[[embed]]` navigation, a command
palette, a notifications inbox — and a built-in **Claude assistant** that
rides the workspace's metered inference proxy and uses tools to read and
act on your workspace.

```
┌ sidebar ──────┬ feed ─────────────────────────────┐
│ ◢ Acme        │ # general  Company-wide announce…  │
│   ⌂ home      │ ── Mon, Aug 3 ──                   │
│   ◉ inbox 4   │ Alice Chen  12:57                  │
│   ☑ my work 2 │ cable smoke test — markdown too    │
│   ▤ documents │ Writer  12:57                      │
│ CHANNELS      │ …                                  │
│ ▸ # general 2 │                                    │
│ AGENTS        │                                    │
│   ◇ researcher│                                    │
│ RECENT        │                                    │
│   ↳ Q3 launch │                                    │
├───────────────┴────────────────────────────────────┤
│ ● LIVE  Acme · saltare cum machina     tab·j/k·q   │
└────────────────────────────────────────────────────┘
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
| `sal doctor` | Diagnose a broken session: config, token store + expiry, server reachability, who the server says you are, and config/server identity drift. Exits non-zero on any failure |
| `sal channels` | List channels (`--json` for scripts, `--kind` to filter) |
| `sal send CHANNEL [MSG]` | Post a message (reads stdin when MSG omitted) |
| `sal tail CHANNEL` | Stream a channel's messages to stdout (`-n` recent history first) |
| `sal search QUERY` | Search messages, tasks, and documents (`--type`, `--channel SLUG`, `--json`) |
| `sal docs` | List documents (`--json`) |
| `sal docs cat SLUG` | Print a document — rendered on a TTY, raw markdown when piped (`--raw`) |
| `sal docs edit SLUG` | Edit a document in `$EDITOR`; conflicts 409 instead of clobbering (`--force`) |
| `sal docs new TITLE` | Create a document (`--body-file PATH`, `-` = stdin) |
| `sal tasks` | List your open tasks (`--all`, `--state S`, `--json`) |
| `sal tasks show SLUG` | Print a task's detail |
| `sal tasks complete SLUG` | Mark a task completed |
| `sal tasks add TITLE` | Create a self-assigned task (`--project SLUG`, `--due YYYY-MM-DD`, `--priority P`) |
| `sal agenda` | Your next 7 days of tasks grouped by day (`--days N`, `--json`) |
| `sal files` | List uploads (`--json`, `--category C`, `-q QUERY`) |
| `sal files put PATH` | Upload a file (`--title T`) — prints the `[[upload:slug]]` embed |
| `sal files get SLUG` | Download a file (`-o PATH`, `--force`) |
| `sal files rm SLUG` | Delete an upload |
| `sal db` | List databases (`--json`) |
| `sal db rows SLUG` | Dump a table — TSV by default, `--csv`, `--json`, `--limit N` |
| `sal ask QUESTION` | Claude answer streamed to stdout, grounded via workspace tools (`--model`, `--no-tools`; reads stdin when QUESTION omitted) |
| `sal version` | Print the version |

## TUI keys

**Home**: sal boots into a dashboard — your unread notification count,
unread channels, and due work (overdue / today / this week). `j/k` move,
`enter` opens the selected row (a channel, a task's detail, or the
notifications inbox), `r` refreshes, `esc`/`q` drops into chat; `ctrl+h`
(or the palette's "home") returns any time — note `ctrl+h` is claimed
globally, so it no longer doubles as backspace in inputs (use `backspace`).
The boot channel keeps its unread badge
while you're on home and gains a `── NEW ──` rule when you enter it.

In chat the composer has focus — just type. `enter` sends, `ctrl+j` inserts
a newline. Typing `@` opens mention autocomplete (`up/down` pick, `tab`/`enter`
complete — names insert exactly as MentionExtractionJob matches them).
`ctrl+y` **attaches a file**: type a path (`~/`, `./`, `/`) and enter
uploads it, type anything else to live-search existing uploads — enter
inserts `[[upload:slug]]` at your cursor either way (also on the palette
as "attach file…").

`tab` cycles composer → sidebar → feed.

**Sidebar**: a **nav rail** (`⌂ home`, `◉ inbox`, `☑ my work`, `▤ documents`,
`⇱ files`, `▦ databases`) sits above the channel list; the rail row for
whatever is on screen reads in arc, and inbox / my work carry unread and
overdue badges. `j/k` move — a channel opens as the cursor lands on it,
while rail rows switch mode only on `enter` (so you can pass over them).
`n`/`N` jump to the next/previous channel with unread messages. Threads and
task discussions you open this session collect under **RECENT**, freshest
first, so a thread stays one keypress away after you leave it. The list
scrolls with the cursor and counts what's off-screen (`↑ 12 more`).
Feed focus is **selection mode**: `j/k` moves a message cursor (arc gutter
bar); `t` opens the selected message's thread, or arms *reply-in-new-thread*
if it has none — your reply creates the thread and the view follows it.
`e` edits your own selected message in the composer (enter saves, esc
cancels and brings back whatever you were typing); `d` asks `y/n` in the
status bar and deletes; `y` copies the message's web permalink (OSC 52, so
it works over SSH and in tmux). `ctrl+t` opens the thread picker; inside a
thread `esc` returns to the parent. `pgup/pgdn` scroll from any focus.
`ctrl+r` refresh · `ctrl+c` quit.

**Search**: `ctrl+f` searches the whole workspace; `/` in feed selection
mode pre-scopes to the current channel. Results group into messages, tasks,
and documents — `enter` jumps to a message or the tasks pane, `y` copies a
permalink or `[[embed]]` reference. Queries debounce as you type.

**Follow references**: `enter` on a selected message follows its
`[[type:slug]]` embeds — a doc opens the reader, a task its detail pane,
channels and messages jump there, an agent lands in the DM. Several embeds
in one message open a picker.

**Documents** (`ctrl+o`): browse, read (glamour-rendered), and edit. `e`
suspends sal into `$VISUAL`/`$EDITOR` (`vi` fallback); the buffer lives at
`~/.config/saltare/edit/` so a crashed editor never loses work, and saves
carry a concurrency guard — if someone edited the doc while you had it
open, sal shows a conflict prompt instead of overwriting them. `n` creates
a document and drops straight into your editor. Requires a session minted
after documents:write joined the CLI grant — if sal says re-run
`sal login`, do that.

**Tasks**: `enter` on a task opens its detail (dates, priority, rendered
description); from there `enter`/`o` drops into the task's **discussion
channel** (joining you so unread tracking works), `x` completes, `s`
cycles the state, `y` copies the `[[task:slug]]` embed. The palette's
**"agenda: next 7 days"** regroups the same pane by day — Overdue /
Today / Tomorrow / weekday — with the same detail and complete keys
(server-filtered via the tasks index's `due_before`, so it stays correct
in big workspaces).

Home, agenda, and attach need no new scopes — an existing `sal login`
session just works (the first surface expansion since the assistant for
which that's true).

**Files & data**: the palette opens both as full TUI views. **Files**:
browse uploads, `d` downloads to your current directory, `u` uploads by
typed path (`~` works), `x` deletes behind a confirm, `y` copies the
embed. **Databases**: pick a table, browse it as a real grid (`h/l` pans
columns, `o` loads more rows), `enter` a row for its detail — and `enter`
a field to **edit the cell in place** (the server types your input per
column; formula columns are read-only). Search results and
`[[upload:…]]`/`[[db:…]]` embeds jump straight into both views.

From scripts, the same power: `sal files put deploy.log` from any server
(downloads follow the presigned storage redirect — your token never
leaves the app host — and never leave truncated files behind);
`sal db rows crm --csv` for spreadsheets, `--json | jq '.[].data'` for
typed cells. Writes need a session minted after their scopes joined the
CLI grant — if sal says re-run `sal login`, one login fixes everything.

Unsent composer text is a **draft**: it survives channel switches, quits,
and crashes (`~/.config/saltare/drafts.json`) and clears when you send.
Opening a channel with unreads draws a `── NEW ──` rule where you left off,
and DMs are labeled with the *other* person's name.

`ctrl+k` opens the **command palette**: fuzzy-jump to any channel or agent, or
run actions (assistant, tasks views, new task, notifications). `ctrl+n` opens
the **notifications inbox** — `enter` jumps to the channel, `R` marks all read.

`ctrl+g` toggles the **assistant** — a local Claude session over the
workspace's inference proxy (the server holds the provider key and meters
credits; the conversation itself never leaves your terminal). It has
**tools**: search, channels, messages, tasks, and documents, all executed
client-side against the REST API with *your* token — so everything it reads
respects your permissions and any task it creates is assigned to you.
Tool calls trace as dim `◇` lines; token usage sums every hop of the loop.
In the feed, `o` loads older history without losing your scroll position,
and `[[type:slug]]` embeds render as `⟨type:slug⟩` chips.

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
- **REST**: channels (+ per-caller unread state and viewer-relative DM
  names), messages (post, edit, delete), search, tasks (+ discussion
  find-or-create), documents (read/write with an optimistic-concurrency
  guard), mark-read.
- **Live**: `/cable?access_token=…` with a same-origin `Origin` header
  (Action Cable forgery protection requires one). Subscribes `MessagesChannel`
  per member channel; consumes `message_created` / `message_updated` /
  `message_destroyed` JSON events; reconnects with backoff and gap-fills over
  REST after every reconnect.

## Layout

```
cmd/sal/          entrypoint + subcommands
internal/api/     typed /api/v1 client (auth, refresh single-flight, resources,
                  Anthropic-format inference streaming with tool blocks)
internal/assist/  the assistant's tool loop: catalog, REST executor, RunLoop
internal/cable/   minimal Action Cable client (subscribe, watchdog, backoff)
internal/store/   in-memory timeline + unread state (single-goroutine, tested)
internal/ui/      Bubble Tea model, NieR HUD lipgloss theme, glamour markdown
internal/config/  config file + drafts + keychain token storage
```

## Development

```sh
go build ./...   # compile
go vet ./...     # static checks
go test ./...    # unit tests (api refresh flow, cable framing, store)
gofmt -l .       # formatting (CI enforces)
```

**tmux + vim-tmux-navigator users**: that setup binds `C-h/j/k/l` globally
and only forwards them to whitelisted programs — so sal's `ctrl+k` (palette)
and `ctrl+j` (newline) silently become pane navigation. Add `sal` to the
process regex the way `fzf` is usually whitelisted — and if the navigator is
loaded as a **TPM plugin**, the plugin re-binds the keys with its own regex
when TPM initializes, so the whitelist must be declared *after* the
`run '~/.tmux/plugins/tpm/tpm'` line or it will be silently clobbered:

```tmux
is_vim_or_sal="ps -o state= -o comm= -t '#{pane_tty}' \
    | grep -iqE '^[^TXZ ]+ +(\\S+\\/)?g?(view|l?n?vim?x?|fzf|sal)(diff)?$'"
bind-key -n 'C-h' if-shell "$is_vim_or_sal" 'send-keys C-h' 'select-pane -L'
bind-key -n 'C-j' if-shell "$is_vim_or_sal" 'send-keys C-j' 'select-pane -D'
bind-key -n 'C-k' if-shell "$is_vim_or_sal" 'send-keys C-k' 'select-pane -U'
bind-key -n 'C-l' if-shell "$is_vim_or_sal" 'send-keys C-l' 'select-pane -R'
```

Verify with `tmux list-keys -T root | grep C-k` — the binding must contain
`sal`.

The TUI is dark-terminal-only for now, matching the NieR HUD design system.
Tested against the dev server: `bin/rails server`, then
`sal login --server http://localhost:3000`.
