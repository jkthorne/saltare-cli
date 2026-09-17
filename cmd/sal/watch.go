package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/cable"
	"github.com/jkthorne/saltare-cli/internal/config"
	"github.com/jkthorne/saltare-cli/internal/store"
	"github.com/jkthorne/saltare-cli/internal/watch"
	"github.com/jkthorne/saltare-cli/internal/weblink"
)

// exitAlreadyRunning is distinct from a plain failure so a supervisor, or a
// user who started one by hand, can tell "someone else has this" from "it
// broke".
const exitAlreadyRunning = 3

// notifyDebounce is how long a burst of cable traffic is allowed to settle
// before the daemon asks the server what it means. The socket says *that*
// something happened; /notifications says *what*, and asking once per message
// in a busy channel would be a request per keystroke of someone else's typing.
const notifyDebounce = 250 * time.Millisecond

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	statePath := fs.String("state", "", "state file (default $XDG_STATE_HOME/saltare/watch.json)")
	ndjson := fs.Bool("ndjson", false, "emit one JSON event per line on stdout")
	once := fs.Bool("once", false, "publish a single snapshot and exit")
	noCable := fs.Bool("no-cable", false, "poll only — do not open the realtime socket")
	poll := fs.Duration("poll", 60*time.Second, "full re-sync interval")
	notify := fs.Bool("notify", false, "raise a desktop notification when someone addresses you")
	notifyAll := fs.Bool("notify-all", false, "notify for every kind, including workspace alerts")
	install := fs.Bool("install-service", false, "write and start the systemd user service, then exit")
	uninstall := fs.Bool("uninstall-service", false, "stop and remove the systemd user service, then exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	switch {
	case *install:
		return installService()
	case *uninstall:
		return uninstallService()
	}

	path := *statePath
	if path == "" {
		resolved, err := watch.StatePath()
		if err != nil {
			return err
		}
		path = resolved
	}

	cfg, client, err := session()
	if err != nil {
		return err
	}

	w := &watcher{
		cfg:    cfg,
		client: client,
		store:  store.New(),
		writer: watch.NewWriter(path),
		ndjson: *ndjson,
		out:    json.NewEncoder(os.Stdout),
		seen:   map[int64]bool{},
		inputs: watch.Inputs{
			Server:    cfg.ServerURL,
			Workspace: watch.Workspace{Slug: cfg.WorkspaceSlug, Name: cfg.WorkspaceName},
			User:      watch.User{ID: cfg.UserID, Name: cfg.UserName},
			Session:   watch.SessionOK,
		},
	}

	if *notify || *notifyAll {
		w.onNew = newNotifier(
			weblink.Links{Server: cfg.ServerURL, Workspace: cfg.WorkspaceSlug},
			*notifyAll,
		).notify
	}

	ctx, cancel := interruptContext()
	defer cancel()

	if *once {
		w.resync(ctx)
		return w.publish()
	}

	// One watcher per user session. A second one would open a second socket and
	// toast every mention twice; refusing is what lets the plugin's README say
	// "enable the service" without worrying about the copy you left in a tmux
	// pane three days ago.
	release, heldBy, err := lockSession()
	if err != nil {
		return err
	}
	if release == nil {
		fmt.Fprintf(os.Stderr, "sal: watch is already running%s\n", pidSuffix(heldBy))
		os.Exit(exitAlreadyRunning)
	}
	defer release()

	return w.run(ctx, *poll, *noCable)
}

type watcher struct {
	cfg    *config.Config
	client *api.Client
	store  *store.Store
	writer *watch.Writer
	inputs watch.Inputs

	ndjson bool
	out    *json.Encoder

	// seen is every notification id this process has already accounted for.
	// It is primed on the first poll *without* emitting, because a daemon that
	// starts at 09:00 must not announce every mention from yesterday.
	seen   map[int64]bool
	primed bool

	// onNew is where --notify hangs its toasts.
	onNew func(watch.Notification)

	// subscribe adds a channel to the live socket. Nil until the cable is up,
	// and nil forever under --no-cable.
	subscribe func(int64)
}

// subscribeKnown tells the socket about every channel the store now holds.
// Called after each resync, because a resync is the only way a new channel is
// ever discovered. Subscribe is idempotent, so re-offering the whole set is
// cheaper than tracking which ones are new.
func (w *watcher) subscribeKnown() {
	if w.subscribe == nil {
		return
	}
	for _, id := range w.channelIDs() {
		w.subscribe(id)
	}
}

func (w *watcher) run(ctx context.Context, pollEvery time.Duration, noCable bool) error {
	w.resync(ctx)
	if err := w.publish(); err != nil {
		return err
	}
	w.emit(map[string]any{
		"ev": "ready", "state": w.inputs.Session,
		"server": w.cfg.ServerURL, "workspace": w.cfg.WorkspaceSlug,
	})
	if w.inputs.Session == watch.SessionLoggedOut {
		return nil
	}

	var events <-chan cable.Event
	if !noCable {
		c := cable.NewClient(w.cfg.ServerURL, w.client.AccessToken, w.channelIDs())
		// Channels that appear after this point — a fresh agent DM, a thread
		// someone started, a channel you were added to — are exactly the ones
		// worth hearing about immediately, and the socket knows nothing about
		// them until told. Without this they surface only on the poll tick.
		w.subscribe = c.Subscribe
		w.subscribeKnown()
		go c.Run(ctx)
		events = c.Events()
	}

	// Stopped, not ticking: a debounce that fires on an idle workspace is a
	// request per quarter second for the rest of the day.
	settle := time.NewTimer(time.Hour)
	if !settle.Stop() {
		<-settle.C
	}
	defer settle.Stop()

	poll := time.NewTicker(pollEvery)
	defer poll.Stop()
	heartbeat := time.NewTicker(watch.HeartbeatSec * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case ev := <-events:
			switch ev.Type {
			case cable.EventConnected:
				// Not a status light: cable.Client's own contract says
				// broadcasts during an outage are lost and the consumer must
				// gap-fill via REST. Miss this and a ten-minute suspend leaves
				// the badge confidently wrong.
				w.inputs.Live = true
				w.resync(ctx)
			case cable.EventDisconnected:
				w.inputs.Live = false
			case cable.EventMessageCreated:
				if !watch.Countable(ev.Message, w.inputs.User.ID) {
					continue
				}
				w.store.Apply(ev)
				settle.Reset(notifyDebounce)
			case cable.EventSubscriptionRejected:
				w.resync(ctx)
			}

		case <-settle.C:
			w.pollNotifications(ctx)

		case <-poll.C:
			w.resync(ctx)

		case <-heartbeat.C:
			// Content is usually unchanged here; Publish turns that into a
			// touch, which is what a reader needs to tell quiet from dead.
		}

		if err := w.publish(); err != nil {
			return err
		}
		if w.inputs.Session == watch.SessionLoggedOut {
			return nil
		}
	}
}

// resync refetches everything. It is the answer to a reconnect, to a poll
// tick, and to a rejected subscription — all three mean "what is on screen may
// have drifted from the server".
func (w *watcher) resync(ctx context.Context) {
	channels, err := w.client.Channels(ctx, api.ChannelsOpts{})
	if err != nil {
		w.degrade(err)
		return
	}
	w.store.SetChannels(channels)
	w.inputs.Channels = channels

	notifications, err := w.client.Notifications(ctx, true)
	if err != nil {
		w.degrade(err)
		return
	}
	w.inputs.Notifications = notifications

	tasks, err := w.client.Tasks(ctx, api.TasksOpts{Mine: true})
	if err != nil {
		w.degrade(err)
		return
	}
	w.inputs.Tasks = tasks

	w.recover()
	w.subscribeKnown()
	w.noteNew(notifications)
}

// pollNotifications is the cheap half of resync, for when the socket has told
// us something arrived and we only need to know what.
func (w *watcher) pollNotifications(ctx context.Context) {
	notifications, err := w.client.Notifications(ctx, true)
	if err != nil {
		w.degrade(err)
		return
	}
	w.inputs.Notifications = notifications
	w.recover()
	w.noteNew(notifications)
}

// noteNew reports notifications this process has not seen. The first call only
// primes the set: everything unread at startup is a backlog, not news.
func (w *watcher) noteNew(notifications []api.Notification) {
	fresh := make([]api.Notification, 0, len(notifications))
	for _, n := range notifications {
		if w.seen[n.ID] {
			continue
		}
		w.seen[n.ID] = true
		fresh = append(fresh, n)
	}
	if !w.primed {
		w.primed = true
		return
	}
	// Server order is newest first; announce oldest first so a burst reads in
	// the order it happened.
	for i := len(fresh) - 1; i >= 0; i-- {
		n := watch.ToNotification(fresh[i])
		w.emit(map[string]any{
			"ev": "notification", "id": n.ID, "action": n.Action, "actor": n.Actor,
			"description": n.Description, "channel": n.ChannelSlug, "preview": n.Preview,
			"task": n.TaskSlug,
		})
		if w.onNew != nil {
			w.onNew(n)
		}
	}
}

// degrade records a failed call without discarding what is already on screen.
//
// Three outcomes, and the distinction is the point. An expired session is
// terminal and needs a human. A server that *answered* and refused — a plan
// limit, a missing scope, a 500 — is not a connection problem, and reporting
// it as one sends someone to restart their router. Everything else is weather.
func (w *watcher) degrade(err error) {
	if errors.Is(err, api.ErrAuthExpired) {
		w.inputs.Session = watch.SessionLoggedOut
		w.inputs.Error = authError(w.cfg, err).Error()
		return
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		w.inputs.Session = watch.SessionBlocked
		w.inputs.Error = err.Error()
		return
	}
	w.inputs.Session = watch.SessionUnreachable
	w.inputs.Error = err.Error()
}

func (w *watcher) recover() {
	w.inputs.Session = watch.SessionOK
	w.inputs.Error = ""
}

func (w *watcher) channelIDs() []int64 {
	channels := w.store.Channels()
	ids := make([]int64, 0, len(channels))
	for _, c := range channels {
		ids = append(ids, c.ID)
	}
	return ids
}

func (w *watcher) publish() error {
	w.inputs.Now = time.Now()
	w.inputs.Unread = map[int64]int{}
	for _, c := range w.inputs.Channels {
		w.inputs.Unread[c.ID] = w.store.Unread(c.ID)
	}

	state := watch.Reduce(w.inputs)
	changed, err := w.writer.Publish(state)
	if err != nil {
		return err
	}
	if changed {
		w.emit(map[string]any{
			"ev": "totals", "state": state.Session, "live": state.Live,
			"unread": state.Totals.Unread, "mentions": state.Totals.Mentions,
			"notifications": state.Totals.Notifications,
			"overdue":       state.Totals.Overdue, "due_today": state.Totals.DueToday,
		})
	}
	return nil
}

func (w *watcher) emit(event map[string]any) {
	if !w.ndjson {
		return
	}
	_ = w.out.Encode(event)
}

// lockSession takes an exclusive advisory lock in the runtime directory, which
// the OS clears on reboot and on process death — so a killed daemon never
// leaves a stale lock that needs explaining.
//
// A nil release with a nil error means somebody else holds it; heldBy is
// whatever pid that somebody wrote, or "" when the file says nothing useful.
// Reporting rather than exiting keeps the decision with the caller, and keeps
// this testable.
func lockSession() (release func(), heldBy string, err error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "saltare")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, "watch.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, "", err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		held, _ := os.ReadFile(path)
		f.Close()
		return nil, leadingDigits(string(held)), nil
	}
	if err := f.Truncate(0); err == nil {
		fmt.Fprintf(f, "%d\n", os.Getpid())
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		os.Remove(path)
	}, "", nil
}

func pidSuffix(pid string) string {
	if pid == "" {
		return ""
	}
	return " (pid " + pid + ")"
}

func leadingDigits(s string) string {
	out := ""
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		out += string(r)
	}
	return out
}
