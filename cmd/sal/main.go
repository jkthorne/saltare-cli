// sal — Saltare in your terminal.
//
//	sal                 launch the TUI
//	sal login           sign in (device session; tokens in the OS keychain)
//	sal logout          revoke the device session
//	sal channels        list channels (--json for scripts)
//	sal tail <channel>  print a channel's messages live to stdout
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"golang.org/x/term"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/cable"
	"github.com/jkthorne/saltare/cli/internal/config"
	"github.com/jkthorne/saltare/cli/internal/ui"
)

// version is stamped by goreleaser via -ldflags "-X main.version=…".
var version = "0.2.0-dev"

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	var err error
	switch cmd {
	case "":
		err = runTUI()
	case "login":
		err = runLogin(args)
	case "logout":
		err = runLogout()
	case "channels":
		err = runChannels(args)
	case "send":
		err = runSend(args)
	case "tasks":
		err = runTasks(args)
	case "ask":
		err = runAsk(args)
	case "tail":
		err = runTail(args)
	case "version", "--version", "-v":
		fmt.Println("sal " + version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "sal: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sal: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`sal — Saltare in your terminal

usage:
  sal                  launch the TUI
  sal login            sign in and store a device session
       --server URL    (default http://localhost:3000)
       --email E       --workspace SLUG   --password-stdin
  sal logout           revoke this device's session
  sal channels         list channels      --json
  sal send CHANNEL MSG post a message (reads stdin when MSG omitted)
  sal tail CHANNEL     stream a channel's messages to stdout
  sal tasks            list your open tasks   --all --state S --json
  sal tasks complete SLUG   mark a task completed
  sal tasks add TITLE       create a task     --project SLUG
  sal ask QUESTION     ask Claude via the workspace inference proxy
                            --model M (default claude-haiku-4-5)
  sal version
`)
}

// ── session helpers ─────────────────────────────────────────────────────

// session loads config + tokens and builds an API client that persists
// rotated tokens back to the keychain.
func session() (*config.Config, *api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	tokens, err := config.LoadTokens()
	if err != nil {
		return nil, nil, err
	}
	client, err := api.New(cfg.ServerURL, tokens)
	if err != nil {
		return nil, nil, err
	}
	client.OnTokens = func(t config.Tokens) { _ = config.SaveTokens(t) }
	return cfg, client, nil
}

func interruptContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// ── commands ────────────────────────────────────────────────────────────

func runTUI() error {
	cfg, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	program := tea.NewProgram(ui.NewModel(ctx, cfg, client), tea.WithAltScreen())
	_, err = program.Run()
	return err
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "http://localhost:3000", "Saltare server URL")
	email := fs.String("email", "", "account email")
	workspace := fs.String("workspace", "", "workspace slug (for multi-workspace accounts)")
	passwordStdin := fs.Bool("password-stdin", false, "read the password from stdin (for scripts)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)
	if *email == "" {
		fmt.Print("email: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		*email = strings.TrimSpace(line)
	}

	var password string
	if *passwordStdin {
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("reading password from stdin: %w", err)
		}
		password = strings.TrimRight(line, "\r\n")
	} else {
		fmt.Print("password: ")
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		password = string(raw)
	}

	// Reuse this install's device id so re-login replaces the session
	// instead of stacking new ones; remember the previous workspace so the
	// picker can offer it as the default.
	deviceID, prevWorkspace := "", ""
	if prev, err := config.Load(); err == nil {
		deviceID = prev.DeviceID
		prevWorkspace = prev.WorkspaceSlug
	}
	if deviceID == "" {
		deviceID = uuid.NewString()
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "terminal"
	}

	ctx, cancel := interruptContext()
	defer cancel()

	params := api.LoginParams{
		ServerURL:     *server,
		Email:         *email,
		Password:      password,
		WorkspaceSlug: *workspace,
		DeviceID:      deviceID,
		DeviceName:    hostname,
	}
	sess, err := api.Login(ctx, params)

	var choice *api.WorkspaceSelectionError
	if errors.As(err, &choice) {
		slug, perr := pickWorkspace(reader, os.Stdout, choice.Choices, prevWorkspace)
		if perr != nil {
			return perr
		}
		params.WorkspaceSlug = slug
		sess, err = api.Login(ctx, params)
	}
	if err != nil {
		return err
	}

	cfg := &config.Config{
		ServerURL:     strings.TrimRight(*server, "/"),
		WorkspaceSlug: sess.Workspace.Slug,
		WorkspaceName: sess.Workspace.Name,
		Email:         sess.User.Email,
		DeviceID:      deviceID,
		UserID:        sess.User.ID,
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	if err := config.SaveTokens(config.Tokens{
		AccessToken:      sess.AccessToken,
		RefreshToken:     sess.RefreshToken,
		AccessExpiresAt:  sess.ExpiresAt,
		RefreshExpiresAt: sess.RefreshExpiresAt,
	}); err != nil {
		return err
	}

	fmt.Printf("signed in to %s as %s — run `sal` to start\n", sess.Workspace.Name, sess.User.Email)
	return nil
}

// pickWorkspace prompts until a valid selection: a list number, a slug, or
// enter for the default (the previously used workspace, when listed). Bad
// input re-prompts — it must never abort the login and force email+password
// again. EOF (piped stdin running dry, ctrl+d) returns an error.
func pickWorkspace(in *bufio.Reader, out io.Writer, choices []api.WorkspaceChoice, defaultSlug string) (string, error) {
	def := ""
	for _, w := range choices {
		if w.Slug == defaultSlug {
			def = w.Slug
		}
	}

	fmt.Fprintln(out, "\nyour account belongs to several workspaces:")
	for i, w := range choices {
		marker := "  "
		if w.Slug == def {
			marker = "* "
		}
		fmt.Fprintf(out, "  %s%d. %s (%s)\n", marker, i+1, w.Name, w.Slug)
	}
	fmt.Fprintln(out, "  tip: `sal login --workspace SLUG` skips this prompt")

	for {
		if def != "" {
			fmt.Fprintf(out, "workspace number or slug [%s]: ", def)
		} else {
			fmt.Fprint(out, "workspace number or slug: ")
		}

		line, readErr := in.ReadString('\n')
		input := strings.TrimSpace(line)

		switch {
		case input == "" && def != "":
			return def, nil
		case input != "":
			if n, err := strconv.Atoi(input); err == nil {
				if n >= 1 && n <= len(choices) {
					return choices[n-1].Slug, nil
				}
				fmt.Fprintf(out, "  pick a number between 1 and %d\n", len(choices))
			} else {
				for _, w := range choices {
					if strings.EqualFold(input, w.Slug) {
						return w.Slug, nil
					}
				}
				fmt.Fprintf(out, "  %q is not a listed number or slug\n", input)
			}
		}

		if readErr != nil {
			return "", fmt.Errorf("no workspace selected")
		}
	}
}

func runLogout() error {
	cfg, client, err := session()
	if err != nil {
		if errors.Is(err, config.ErrNotConfigured) || errors.Is(err, config.ErrNoTokens) {
			return config.DeleteTokens()
		}
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	if err := client.SignOut(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "sal: server sign-out failed ("+err.Error()+"); clearing local session anyway")
	}
	if err := config.DeleteTokens(); err != nil {
		return err
	}
	fmt.Printf("signed out of %s\n", cfg.WorkspaceName)
	return nil
}

func runChannels(args []string) error {
	fs := flag.NewFlagSet("channels", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print raw JSON")
	kind := fs.String("kind", "", "filter by kind")
	if err := fs.Parse(args); err != nil {
		return err
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	channels, err := client.Channels(ctx, api.ChannelsOpts{Kind: *kind})
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(channels)
	}
	for _, c := range channels {
		unread := ""
		if n := c.Unread(); n > 0 {
			unread = fmt.Sprintf("  (%d unread)", n)
		}
		member := " "
		if c.Member {
			member = "*"
		}
		fmt.Printf("%s %-18s %-16s %s%s\n", member, c.Slug, c.Kind, c.Name, unread)
	}
	return nil
}

func runSend(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: sal send CHANNEL [MESSAGE]")
	}
	slug := args[0]

	var body string
	if len(args) > 1 {
		body = strings.Join(args[1:], " ")
	} else {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		body = strings.TrimSpace(string(raw))
	}
	if body == "" {
		return fmt.Errorf("empty message")
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	msg, err := client.SendMessage(ctx, slug, body)
	if err != nil {
		return err
	}
	fmt.Printf("sent #%d to %s\n", msg.ID, slug)
	return nil
}

func runTasks(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "complete":
			if len(args) != 2 {
				return fmt.Errorf("usage: sal tasks complete SLUG")
			}
			return taskComplete(args[1])
		case "add":
			return taskAdd(args[1:])
		}
	}

	fs := flag.NewFlagSet("tasks", flag.ExitOnError)
	all := fs.Bool("all", false, "everyone's tasks, not just yours")
	state := fs.String("state", "", "filter by state (open, in_progress, waiting, completed, cancelled)")
	asJSON := fs.Bool("json", false, "print raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	tasks, err := client.Tasks(ctx, api.TasksOpts{Mine: !*all, State: *state})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	}
	for _, t := range tasks {
		due := ""
		if t.DueDate != nil {
			due = "  due " + *t.DueDate
		}
		fmt.Printf("%-12s %-24s %s%s\n", t.State, t.Slug, t.Title, due)
	}
	return nil
}

func taskComplete(slug string) error {
	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	task, err := client.UpdateTaskState(ctx, slug, "completed")
	if err != nil {
		return err
	}
	fmt.Printf("completed: %s\n", task.Title)
	return nil
}

func taskAdd(args []string) error {
	fs := flag.NewFlagSet("tasks add", flag.ExitOnError)
	projectSlug := fs.String("project", "", "project slug (defaults to the only/first project)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	title := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if title == "" {
		return fmt.Errorf("usage: sal tasks add TITLE [--project SLUG]")
	}

	cfg, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	projects, err := client.Projects(ctx)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return fmt.Errorf("no projects in this workspace — create one on the web first")
	}
	project := projects[0]
	if *projectSlug != "" {
		found := false
		for _, p := range projects {
			if p.Slug == *projectSlug {
				project, found = p, true
				break
			}
		}
		if !found {
			return fmt.Errorf("project %q not found", *projectSlug)
		}
	}

	task, err := client.CreateTask(ctx, project.ID, title, cfg.UserID)
	if err != nil {
		return err
	}
	fmt.Printf("created %s in %s: %s\n", task.Slug, project.Name, task.Title)
	return nil
}

func runAsk(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	model := fs.String("model", "claude-haiku-4-5", "Claude model id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	question := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if question == "" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		question = strings.TrimSpace(string(raw))
	}
	if question == "" {
		return fmt.Errorf("usage: sal ask QUESTION")
	}

	cfg, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	req := api.InferenceRequest{
		Model:  *model,
		System: "You are sal, answering one-shot questions in a terminal for the Saltare workspace \"" + cfg.WorkspaceName + "\". Be concise; plain text only.",
		Messages: []api.InferenceMessage{
			{Role: "user", Content: question},
		},
	}
	res, err := client.StreamInference(ctx, req, func(delta string) {
		fmt.Print(delta)
	})
	fmt.Println()
	if err != nil {
		return err
	}
	if res != nil {
		fmt.Fprintf(os.Stderr, "· %s · %d in → %d out tokens\n", *model, res.Usage.InputTokens, res.Usage.OutputTokens)
	}
	return nil
}

func runTail(args []string) error {
	// Go's flag package stops at the first positional arg, so accept the slug
	// leading (`sal tail general -n 3`) or trailing (`sal tail -n 3 general`).
	slug := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	history := fs.Int("n", 10, "recent messages to print first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if slug == "" && fs.NArg() > 0 {
		slug = fs.Arg(0)
	}
	if slug == "" {
		return fmt.Errorf("usage: sal tail CHANNEL")
	}

	cfg, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	channels, err := client.Channels(ctx, api.ChannelsOpts{})
	if err != nil {
		return err
	}
	var target *api.Channel
	for i := range channels {
		if channels[i].Slug == slug {
			target = &channels[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("channel %q not found", slug)
	}

	if *history > 0 {
		msgs, err := client.Messages(ctx, slug, 1, *history)
		if err != nil {
			return err
		}
		for i := len(msgs) - 1; i >= 0; i-- {
			printTailMessage(msgs[i])
		}
	}

	cableClient := cable.NewClient(cfg.ServerURL, client.AccessToken(), []int64{target.ID})
	go cableClient.Run(ctx)

	fmt.Fprintf(os.Stderr, "── tailing #%s (ctrl+c to stop)\n", slug)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-cableClient.Events():
			switch ev.Type {
			case cable.EventMessageCreated:
				printTailMessage(*ev.Message)
			case cable.EventDisconnected:
				fmt.Fprintln(os.Stderr, "── connection lost; retrying …")
			case cable.EventConnected:
				fmt.Fprintln(os.Stderr, "── live")
			}
		}
	}
}

func printTailMessage(m api.Message) {
	if m.IsSystemEvent() {
		fmt.Printf("%s · %s\n", m.CreatedAt.Local().Format("15:04"), *m.SystemEvent)
		return
	}
	name := m.Sender.Name
	if name == "" {
		name = fmt.Sprintf("%s#%d", strings.ToLower(m.Sender.Type), m.Sender.ID)
	}
	fmt.Printf("%s %s: %s\n", m.CreatedAt.Local().Format("15:04"), name, m.Body)
}
