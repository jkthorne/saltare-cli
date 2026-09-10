# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository.

**What this is:** `sal`, the terminal client for Saltare — a Go TUI + CLI that
talks to a Saltare server over `/api/v1` and `/cable`. The server lives in the
sibling checkout `../saltare` (Rails); the other hand-written clients of the
same API are `../saltare-ios` (Swift) and `../saltare-sdk` (Kotlin).

**Status:** not in production, no live users. Prefer clean breaks over
compatibility shims — but note the one thing that *is* a compatibility surface:
token rotation copies a device session's existing scopes, so widening the `CLI`
grant server-side only reaches sessions minted afterwards. The client must
handle the `missing_scope` error code rather than assume its scopes match the
current constant.

## Development commands

```bash
go build ./...             # build every package
go build -o sal ./cmd/sal  # build the binary
go vet ./...
go test ./...
go test ./internal/ui/ -run TestSidebar   # one test
gofmt -l .                 # must print nothing; CI enforces
```

Against a local server: from `../saltare`, `bin/rails server`, then
`./sal login --server http://localhost:3000`.

Releases: push a `v*` tag → `.github/workflows/release.yml` runs goreleaser
(`.goreleaser.yaml`).

## The API contract — read before touching `internal/api`

The server's serializers are hand-written Ruby and these structs are
hand-written Go. Nothing generates either, so a renamed server key is a silent
zero value in a shipped binary rather than a build error.

`internal/api/testdata/api_golden/*.json` are copies of the server's golden
payloads (one per serializer) and `internal/api/golden_test.go` asserts that
every field these structs declare still has a key in them. Server keys sal
ignores are logged, not failed.

When the server's serializers change:

1. In `../saltare`: `WRITE_GOLDEN=1 bin/rails test test/serializers/api/v1/golden_payloads_test.rb`
2. Here: `script/sync-goldens.sh && go test ./internal/api/`
3. Fix whatever the golden test reports.

The full contract — auth doors, scope grants, error codes, fleet sign-on — is
`docs/native-clients.md` in `../saltare`.

## Architecture

Go TUI + CLI (`sal`) (Bubble Tea/Lip Gloss/Glamour + coder/websocket; module `github.com/jkthorne/saltare-cli`). `sal login` mints a **user-bound** device session (`platform: "cli"` → no per-device agent, posts attribute to the human) with the `ApiKey::Scopes::CLI` grant (DEVICE + `tasks:write` + `notifications:read` + `documents:write` + `uploads:write` + `databases:write`; mobile keeps DEVICE; **rotation copies old scopes — widening the grant requires users to re-login**, and the Go client maps `missing_scope` to that hint). TUI: composer (enter sends, ctrl+j newline) with @-mention autocomplete (`GET /api/v1/mentionables`) and **per-channel drafts** (stash/restore on switch, persisted to `~/.config/saltare/drafts.json`); feed selection mode (`t` thread, `e` edit own message in-composer, `d` y/n-confirmed delete, `y` permalink copy via OSC 52 tmux-wrapped); ctrl+t thread picker; agent DMs bootstrapped via `POST /api/v1/agents/:slug/message`; ctrl+k command palette; **ctrl+f workspace search** (`/` in feed pre-scopes to the channel; debounced, generation-guarded; enter jumps, `y` copies permalink/`[[embed]]`); tasks pane; ctrl+n notifications inbox. A `── NEW ──` rule marks the read cursor (snapshotted at open before mark-read fires); DMs label as the counterpart via the serializer's viewer-relative `display_name` (`Channel.Title()` fallback for old servers). **Beyond chat (Phase 6)**: **ctrl+o documents mode** (browser + glamour reader with its own viewport/renderer; `e` suspends via `tea.ExecProcess` into `$VISUAL`/`$EDITOR`/vi with the buffer at `~/.config/saltare/edit/doc-<slug>.md`, saves send `base_updated_at` and a 409 `stale_document` opens an overwrite/keep prompt; `n` creates→edits); **task detail** (enter in the tasks pane → facts + rendered description; `enter`/`o` → `POST /tasks/:slug/discussion` find-or-create+join then `openThread`, `s` cycles state, `x` completes); **follow-embed** (`enter` on a selected message follows `[[doc/task/channel/msg/agent/upload/db:…]]`, multi-embed picker; project toasts to the web); system events render metadata context ("open → in_progress"). **Files/DB views (Phase 8)**: palette-launched — files browse/download (`config.SafeWriteFile` temp+rename)/upload-by-path/delete-confirm; databases list → `bubbles/table` grid (schema-ordered via `internal/tablefmt`, `h/l` column window, `o` pages) → row detail with in-place cell editing (raw strings PATCHed whole-hash via `UpdateRowData`; server coerces; formula read-only); ctrl+f search includes an uploads section (server: `uploads:read` in the `/api/v1/search` type intersection) that jumps into files. **Home/agenda/attach (Phase 9, no new scopes — no re-login)**: sal boots into a client-side **home** dashboard (**ctrl+h** returns to it from anywhere, closing overlays; `viewHome`; unread channels from the store, my-work due buckets from the boot `tasks?mine` fetch, notification count via its own `notifCountMsg` so it can't pop the ctrl+n overlay; boot mark-read is deferred to home→chat entry, which snapshots first so the boot channel gains a `── NEW ──` rule); **agenda** is a tasks-pane mode (palette-launched; `internal/agenda` groups overdue/today/tomorrow/weekday; fetch uses `due_before` and a `fetchGen` guard drops stale mode-switch responses) with a `sal agenda [--days N --json]` twin; **ctrl+y attach** in chat (path-prefixed input streams an upload, anything else live-searches uploads; enter inserts `[[upload:slug]]` at the composer cursor via `textarea.InsertString`). **Left pane (Phase 10, client-only)**: the sidebar is a **nav rail + channel list** (`internal/ui/sidebar.go`) — six rail rows (home/inbox/my work/documents/files/databases) prepended in `rebuildSidebar`, each mapping to the same entry point its palette action uses (`runNav`); the rail row matching what's on screen renders in arc (`activeNavKey`), inbox/my-work carry badges (`railCounts` — the overdue count is suppressed unless the tasks pane holds the my-work list). Rail rows are `sidebarItem{nav:}` — `moveSelection` skips activation for them (channels still open as the cursor lands, rail rows switch mode on enter only), and `firstUnreadItem`/`hasConversation` exist so the boot cursor and the "no channels visible" fatal never mistake a rail row for a feed. `sidebarWindow` scrolls the list with the cursor and overwrites edge lines with `↑/↓ N more` (never the cursor's own line) — before it the pane clipped silently at terminal height. Threads and discussions collect in a **RECENT** group (`recentItems`, `UpdatedAt` desc); `Store.SetChannels` preserves already-held `contextualKinds` (thread/discussion) across a refresh, so a channels reload can't forget the thread being read. `n`/`N` jump to the next/previous unread channel. **Click awareness (Phase 11, client-only)**: `tea.WithMouseCellMotion` is on by default, gated by `Config.Mouse` (`*bool` — absent means on) with `sal --no-mouse`/`--mouse` overriding in memory and a palette toggle (`actionMouse` → `tea.DisableMouse`/`EnableMouseCellMotion`) for mid-session escape, because tracking costs the terminal's own drag-to-select (shift/option-drag recovers it). `internal/ui/layout.go` holds the frame geometry — `paneRects` (sidebar incl. its 1-col border, pane, feed, composer, status) computed at the end of `layout()`, so `View` and hit-testing read one source of truth instead of recomputing `sidebarWidth+1`/`m.height-1` in six places; `handleComposerKey` now re-lays out when the composer's height changes (it previously left the viewport a line short of the frame). `internal/ui/rows.go`'s `rowBuilder` pairs each rendered line with the item index it draws (`chrome`/`row`/`target`/`lineOf`/`replace`/`slice`), because line index ≠ item index wherever group labels, spacers, or a variable preamble intervene — `sidebarBody`/`sidebarWindow` and `homeLines` build on it, and the sidebar's `↑/↓ N more` markers drop their target so a count can't open the row it covers. `internal/ui/mouse.go` dispatches: wheel → the viewport (chat/assistant, docs reader) or `grid.MoveUp/MoveDown` (`bubbles/table` has no mouse support at all); left-press → sidebar row (single click activates, via `openSelected`→`runNav`), feed message (click selects, clicking the already-selected message activates — a deliberate substitute for double-click timing), composer (focus), home row (`activateHomeRow`, shared with enter). Hit maps are **recomputed from the same pure builders View uses**, never cached from the last frame, since unread counts mutate on every cable event. Gated: press-only/left-only, `!m.ready` before the first `WindowSizeMsg`, and `overlayActive()` (palette/search/inbox/attach/pickers stay keyboard-only). `handleEditorFinished` re-issues `EnableMouseCellMotion` — bubbletea's `RestoreTerminal` restores the alt screen and bracketed paste but *not* the mouse, so a doc edit otherwise killed clicking for the session. **OSC 8 hyperlinks** (`internal/ui/hyperlink.go`, independent of mouse tracking — the terminal handles the click): `webLinks` is the single URL builder (zero value → no URLs; `messagePermalink` delegates to it so copied and clicked URLs can't drift), and message timestamps, the channel title, task detail titles, and `task`/`channel`/`agent` chips carry links. `doc`/`upload`/`db`/`msg` chips stay unlinked — they live at data-tree paths sal doesn't carry. `lipgloss.Width` is OSC-aware but `truncate` is not (it rune-slices), so links are applied **outermost, after styling and truncation**. Fixed along the way: embed chips never rendered in the feed at all — `renderEmbedChips` ran *after* glamour, which colours per token and splits `[[` into `[` ESC `[`, so the pattern never matched; chips are now a two-phase `tokenizeEmbeds`/`restoreEmbedChips` (inert alphanumeric marker survives wrapping, lists, headings, inline code) mirroring the server's `EmbedPreprocessor`. Not yet wired: the tasks/docs/files/DB list panes, status bar, overlays, right-click, drag. Built-in assistant (ctrl+g / `sal ask [--no-tools]`): a Claude session through `POST /api/v1/inference/messages` (verbatim Anthropic passthrough — tool blocks round-trip; SSE block-accumulator in `internal/api/inference.go`) **with client-side tools** — `internal/assist/` defines an 8-tool catalog (search/channels/messages/tasks/documents) executed against `/api/v1` with the user's own token (user attribution + user permissions; MCP was rejected because `tools/call` requires agent-bound keys), loop capped at 8 rounds then forced text, history trimming is tool-pair-aware. Scriptable: `sal send`, `sal tail`, `sal channels --json`, `sal search --json`, `sal docs [cat|edit|new]` (cat renders on TTY / raw when piped; edit = guarded `$EDITOR` round-trip, `--force` on conflict), `sal tasks [show|complete|add --due --priority]`, `sal files [put|get|rm]` (streamed multipart up; downloads follow the presigned-storage redirect on a timeout-free client — Go strips the bearer on the cross-hostname hop; temp+rename writes), `sal db [rows --csv|--json --limit]` (read-only; schema-ordered columns, all pages by default), `sal ask`. Releases: push a `v*` tag → `.github/workflows/release.yml` runs goreleaser (`.goreleaser.yaml`, darwin/linux × amd64/arm64, version via ldflags; brew-tap block commented until the tap repo + `HOMEBREW_TAP_TOKEN` exist). Tokens in OS keychain (file fallback); live events via `/cable` JSON `MessagesChannel` (policy-gated visibility, not membership) — the client must send a same-origin `Origin` header (Action Cable forgery protection); edits/deletes fan out from `Message` model callbacks. Dev loop: `go build/vet/test ./...`; CI enforces gofmt+vet+build+test. See `README.md`.

## Conventions

- Standard Go style; `gofmt` is the whole formatter argument.
- No third-party test framework — `testing` and table-driven tests.
- `internal/` for everything but `cmd/sal`; the module publishes no library.
- Bubble Tea's Elm loop: `Update` mutates model state and returns commands, and
  `View` must stay pure — hit maps for the mouse are rebuilt from the same pure
  builders `View` uses, never cached from the last frame.
- Terminal capabilities that bubbletea does not restore for you (mouse
  tracking) must be re-issued after `tea.ExecProcess` returns.
