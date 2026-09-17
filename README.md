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

With Go:

```sh
go install github.com/jkthorne/saltare-cli/cmd/sal@latest
```

Homebrew — the tap still builds from source, a holdover from when this repo was
private:

```sh
brew tap jkthorne/tap https://github.com/jkthorne/homebrew-tap.git
brew install sal
```

Or grab a `sal_*_<os>_<arch>.tar.gz` from a GitHub release. Releases are cut by
pushing a `v*` tag (`.github/workflows/release.yml` runs goreleaser; config in
`.goreleaser.yaml`). After tagging, bump `tag`/`version` in the tap's
`Formula/sal.rb`.

Now that the repo is public, goreleaser can generate binary formulas and push
them to the tap itself — the `brews:` block in `.goreleaser.yaml` is written and
commented out. It needs `HOMEBREW_TAP_TOKEN` set as a repository secret first;
it is not set today, and uncommenting the block without it would fail the next
release rather than publish one.

From source:

```sh
go build -o sal ./cmd/sal
./sal login --server https://your-saltare.example   # or http://localhost:3000
./sal                                               # launch the TUI
```

## Commands

| Command | What it does |
|---|---|
| `sal` | Launch the TUI (`--no-mouse` disables clicks/scrolling, `--mouse` forces them on) |
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
| `sal agents` | List the workspace's agents (`--json`) |
| `sal agents message AGENT [MSG]` | Send to an agent's DM (reads stdin when MSG omitted) |
| `sal open KIND SLUG` | Open a channel, task, agent or message in the browser (`--print`) |
| `sal watch` | Publish unread, mentions and due work to a state file — see below |
| `sal status` | Read that file. No network, no token (`--json`, `--waybar`, `--follow`) |
| `sal version` | Print the version |

## Watching

`sal watch` holds the session and publishes what it knows to
`$XDG_STATE_HOME/saltare/watch.json`. Everything else reads that file: the
[Omarchy widget](https://github.com/jkthorne/saltare-omarchy), a waybar module
(`sal status --waybar --follow`), a shell prompt (`sal status`). They need no
credentials of their own, which is the point — the token stays in one process.

```sh
sal watch --install-service    # systemd user unit, starts on login
sal watch --notify             # desktop toasts for mentions and DMs
```

**It costs API requests, and the budget is small.** The websocket is free — it
never reaches the usage gate — but the backstop poll is metered. Two requests
every five minutes plus a task refresh every thirty is about **18,700 a
month**, which is a third of what the Pro plan includes and more than a starter
workspace gets in total. `--poll` tunes it; the first version of this used a
one-minute poll and spent 129,600 a month idling, which was two and a half
times everything Pro includes.

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
column; formula columns are read-only). Grid headings and detail labels
read the columns' human names; `sal db rows` heads its TSV and CSV with
the column *keys*, because that header is what scripts parse. Search results and
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

## Mouse

Clicks and scrolling work, and everything they do has a keyboard equivalent —
mouse support is additive, so a terminal that doesn't report events (or an
`ssh`/`screen` hop that swallows them) loses nothing.

- **Sidebar**: click a channel, agent, or nav-rail row to open it. One click
  activates, the same as `enter` — there is no double-click to learn.
- **Feed**: click a message to select it (arc gutter bar), then all the
  selection keys apply (`t` thread, `e` edit, `d` delete, `y` permalink).
  Clicking the *already-selected* message follows its `[[embeds]]`, which is
  what `enter` does.
- **Composer**: click it to take focus back from the feed.
- **Home**: click any dashboard row — an unread channel, a due task, the
  notification count.
- **List panes**: click a task to open its detail, a document to read it, a
  table to open its grid. Files moves the cursor and stops, because `enter`
  does nothing there — the row under the cursor is what `d`/`x`/`y` act on.
- **Database grid**: click a row to select it, click it again to open it —
  the feed's bargain, because the grid cursor drives `y` and `o` too.
- **Overlays**: the palette, search, the inbox, attach and the two pickers are
  all pickers, so a click runs the row. The input keeps focus, so you can go on
  typing to narrow the list.
- **Status bar**: click a key hint to press the key it names. `ctrl+c quit` is
  a label only — it is the one hint with no undo.
- **Wheel**: scrolls the chat feed, the document reader, and the database grid.

Two shapes, one rule: where a row is a link, one click follows it; where the
selection is state other keys read, a click sets it and a second click acts. A
click also only ever lands where the keyboard cursor already is — a pane whose
keys have gone to a prompt or a `y/n` confirmation ignores clicks rather than
doing something the keyboard can't.

**The cost, and the escape hatch.** With mouse tracking on, the terminal stops
handling click-drag text selection itself. Most terminals give it back if you
hold **shift** while dragging (**option** on macOS Terminal and iTerm2). If you
would rather not trade it at all:

- `sal --no-mouse` for one run, or `"mouse": false` in
  `~/.config/saltare/config.json` to make it permanent (`--mouse` forces it back
  on for a run).
- The palette (`ctrl+k` → "mouse: on/off") toggles it mid-session, for when you
  just need to drag-select one stack trace.

Not wired yet: right-click menus and drag.

## Hyperlinks

Independent of mouse tracking, sal emits OSC 8 terminal hyperlinks, which
terminals that support them open on ctrl/cmd-click:

- a message's **timestamp** → its web permalink (the same URL `y` copies)
- the **channel title** → the channel on the web
- a **task detail title** → the task page
- `⟨task:…⟩`, `⟨channel:…⟩`, and `⟨agent:…⟩` **chips** → their web pages

Chips for docs, uploads, databases, and `[[msg:id]]` stay unlinked on purpose —
those live at data-tree paths sal doesn't carry, and a dead link is worse than
plain text. Terminals without OSC 8 support discard the sequence and print the
label bare; nothing needs configuring either way.

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

If your tmux mouse configuration fights sal's for clicks or selection, `sal
--no-mouse` (or `"mouse": false` in the config) hands the pointer back to tmux
without giving up any functionality — every mouse action has a key.

Verify with `tmux list-keys -T root | grep C-k` — the binding must contain
`sal`.

The TUI is dark-terminal-only for now, matching the NieR HUD design system.
Tested against the dev server: from a `saltare` checkout `bin/rails server`,
then `sal login --server http://localhost:3000`.

## The API contract

sal decodes `/api/v1` by hand, and so do saltare-ios (Swift) and saltare-sdk
(Kotlin). Nothing generates any of them, so a renamed server key is a silent
zero value here rather than a build error.

`internal/api/testdata/api_golden/*.json` are copies of the server's golden
payloads, one per serializer, and `internal/api/golden_test.go` asserts that
every field these structs declare still has a key in them. Server keys sal
ignores are logged, not failed — a client that decodes less than the server
sends is working as intended.

Refresh the copies when the server's serializers change:

```sh
script/sync-goldens.sh          # from a sibling ../saltare checkout
```

The server regenerates its side with:

```sh
WRITE_GOLDEN=1 bin/rails test test/serializers/api/v1/golden_payloads_test.rb
```

The full contract — auth doors, scope grants, error codes — is
`docs/native-clients.md` in the saltare repo.
