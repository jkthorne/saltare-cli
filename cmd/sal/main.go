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
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/google/uuid"
	"golang.org/x/term"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/assist"
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
	case "search":
		err = runSearch(args)
	case "docs":
		err = runDocs(args)
	case "files":
		err = runFiles(args)
	case "db":
		err = runDB(args)
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
  sal search QUERY     search messages, tasks, and documents
                            --type T --channel SLUG --json
  sal docs             list documents          --json
  sal docs cat SLUG    print a document        --raw (default when piped)
  sal docs edit SLUG   edit a document in $EDITOR   --force on conflicts
  sal docs new TITLE   create a document       --body-file PATH (- = stdin)
  sal files            list uploads            --json --category C -q QUERY
  sal files put PATH   upload a file           --title T
  sal files get SLUG   download a file         -o PATH --force
  sal files rm SLUG    delete an upload
  sal db               list databases          --json
  sal db rows SLUG     dump a table (TSV)      --csv --json --limit N
  sal tasks            list your open tasks   --all --state S --json
  sal tasks show SLUG       print a task's detail
  sal tasks complete SLUG   mark a task completed
  sal tasks add TITLE       create a task     --project SLUG --due YYYY-MM-DD --priority P
  sal ask QUESTION     ask Claude, grounded in your workspace via tools
                            --model M (default claude-haiku-4-5) --no-tools
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
		UserName:      sess.User.Name,
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

func runSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print raw JSON")
	kind := fs.String("type", "", "restrict to messages, tasks, or documents")
	channel := fs.String("channel", "", "channel slug to scope message results")
	limit := fs.Int("limit", 0, "per-type result cap (max 50)")
	query, err := parseTrailing(fs, args)
	if err != nil {
		return err
	}
	if query == "" {
		return fmt.Errorf("usage: sal search QUERY")
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	results, err := client.Search(ctx, query, api.SearchOpts{Type: *kind, Channel: *channel, Limit: *limit})
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	for _, m := range results.Messages {
		body := strings.ReplaceAll(m.Body, "\n", " ")
		fmt.Printf("msg   #%-14s %-14s %s\n", m.Channel.Slug, m.Sender.Name, body)
	}
	for _, t := range results.Tasks {
		fmt.Printf("task  %-15s %-14s %s\n", t.Slug, t.State, t.Title)
	}
	for _, d := range results.Documents {
		fmt.Printf("doc   %-15s %s\n", d.Slug, d.Title)
	}
	for _, u := range results.Uploads {
		fmt.Printf("file  %-15s %s\n", u.Slug, u.Title)
	}
	if len(results.Messages)+len(results.Tasks)+len(results.Documents)+len(results.Uploads) == 0 {
		fmt.Fprintln(os.Stderr, "no results")
	}
	return nil
}

// parseTrailing parses flags that may appear before or after a positional
// query (flag.Parse stops at the first positional arg).
func parseTrailing(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	positional := fs.Args()
	if len(positional) > 0 && strings.HasPrefix(positional[len(positional)-1], "-") {
		return "", fmt.Errorf("flags must come before or directly after the query")
	}
	var words []string
	for len(positional) > 0 {
		words = append(words, positional[0])
		rest := positional[1:]
		if len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			if err := fs.Parse(rest); err != nil {
				return "", err
			}
			positional = fs.Args()
			continue
		}
		positional = rest
	}
	return strings.Join(words, " "), nil
}

func runDocs(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "cat":
			return docsCat(args[1:])
		case "edit":
			return docsEdit(args[1:])
		case "new":
			return docsNew(args[1:])
		}
	}

	fs := flag.NewFlagSet("docs", flag.ExitOnError)
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

	docs, err := client.Documents(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(docs)
	}
	for _, d := range docs {
		published := " "
		if d.Published {
			published = "*"
		}
		fmt.Printf("%s %-28s %-12s %s\n", published, d.Slug, d.UpdatedAt.Local().Format("Jan 2 15:04"), d.Title)
	}
	return nil
}

func docsCat(args []string) error {
	fs := flag.NewFlagSet("docs cat", flag.ExitOnError)
	raw := fs.Bool("raw", false, "print raw markdown even on a TTY")
	slug, err := slugAndFlags(fs, args, "sal docs cat SLUG")
	if err != nil {
		return err
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	doc, err := client.Document(ctx, slug)
	if err != nil {
		return err
	}

	if *raw || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Print(doc.Body)
		if !strings.HasSuffix(doc.Body, "\n") {
			fmt.Println()
		}
		return nil
	}

	width, _, sizeErr := term.GetSize(int(os.Stdout.Fd()))
	if sizeErr != nil || width <= 0 || width > 120 {
		width = 100
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(width-2))
	if err != nil {
		fmt.Println(doc.Body)
		return nil
	}
	out, err := renderer.Render("# " + doc.Title + "\n\n" + doc.Body)
	if err != nil {
		fmt.Println(doc.Body)
		return nil
	}
	fmt.Print(out)
	return nil
}

func docsEdit(args []string) error {
	fs := flag.NewFlagSet("docs edit", flag.ExitOnError)
	force := fs.Bool("force", false, "save even if the document changed on the server")
	slug, err := slugAndFlags(fs, args, "sal docs edit SLUG")
	if err != nil {
		return err
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	doc, err := client.Document(ctx, slug)
	if err != nil {
		return err
	}
	path, err := config.EditBufferPath(doc.Slug)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(doc.Body), 0o600); err != nil {
		return err
	}

	cmd := config.EditorCommand(path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor: %w (your buffer is at %s)", err, path)
	}

	edited, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(edited) == doc.Body {
		_ = os.Remove(path)
		fmt.Println("no changes")
		return nil
	}

	base := doc.UpdatedAt
	if *force {
		base = time.Time{}
	}
	updated, err := client.UpdateDocumentBody(ctx, slug, string(edited), base)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "stale_document" {
			return fmt.Errorf("document changed on the server; your edit is preserved at %s — re-run with --force to overwrite", path)
		}
		return fmt.Errorf("%w (your edit is preserved at %s)", err, path)
	}
	_ = os.Remove(path)
	fmt.Printf("saved %s (updated %s)\n", updated.Slug, updated.UpdatedAt.Local().Format("15:04:05"))
	return nil
}

func docsNew(args []string) error {
	fs := flag.NewFlagSet("docs new", flag.ExitOnError)
	bodyFile := fs.String("body-file", "", "read the body from a file (- = stdin)")
	title, err := parseTrailing(fs, args)
	if err != nil {
		return err
	}
	if title == "" {
		return fmt.Errorf("usage: sal docs new TITLE [--body-file PATH]")
	}

	body := ""
	switch *bodyFile {
	case "":
	case "-":
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		body = string(raw)
	default:
		raw, err := os.ReadFile(*bodyFile)
		if err != nil {
			return err
		}
		body = string(raw)
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	doc, err := client.CreateDocument(ctx, title, body)
	if err != nil {
		return err
	}
	fmt.Printf("created %s: %s\n", doc.Slug, doc.Title)
	return nil
}

// slugAndFlags accepts the slug before or after flags (the runTail idiom).
func slugAndFlags(fs *flag.FlagSet, args []string, usage string) (string, error) {
	slug := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if slug == "" {
		slug = fs.Arg(0)
	}
	if slug == "" {
		return "", fmt.Errorf("usage: %s", usage)
	}
	return slug, nil
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
		case "show":
			if len(args) != 2 {
				return fmt.Errorf("usage: sal tasks show SLUG")
			}
			return taskShow(args[1])
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
	due := fs.String("due", "", "due date, YYYY-MM-DD")
	priority := fs.String("priority", "", "none, low, medium, high, or urgent")
	title, err := parseTrailing(fs, args)
	if err != nil {
		return err
	}
	if title == "" {
		return fmt.Errorf("usage: sal tasks add TITLE [--project SLUG] [--due YYYY-MM-DD] [--priority P]")
	}
	if *due != "" {
		if _, err := time.Parse("2006-01-02", *due); err != nil {
			return fmt.Errorf("--due must be YYYY-MM-DD")
		}
	}
	switch *priority {
	case "", "none", "low", "medium", "high", "urgent":
	default:
		return fmt.Errorf("--priority must be none, low, medium, high, or urgent")
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

	task, err := client.CreateTask(ctx, project.ID, title, cfg.UserID, api.TaskCreateOpts{DueDate: *due, Priority: *priority})
	if err != nil {
		return err
	}
	fmt.Printf("created %s in %s: %s\n", task.Slug, project.Name, task.Title)
	return nil
}

func taskShow(slug string) error {
	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	task, err := client.Task(ctx, slug)
	if err != nil {
		return err
	}

	fmt.Printf("%s  [%s]\n", task.Title, task.State)
	fmt.Printf("slug: %s\n", task.Slug)
	if task.Priority != nil && *task.Priority != "none" {
		fmt.Printf("priority: %s\n", *task.Priority)
	}
	if task.Assignee != nil {
		fmt.Printf("assignee: %s #%d\n", strings.ToLower(task.Assignee.Type), task.Assignee.ID)
	}
	if task.StartDate != nil && *task.StartDate != "" {
		fmt.Printf("start: %s\n", *task.StartDate)
	}
	if task.DueDate != nil && *task.DueDate != "" {
		fmt.Printf("due: %s\n", *task.DueDate)
	}
	if task.SubtasksCount > 0 {
		fmt.Printf("subtasks: %d\n", task.SubtasksCount)
	}
	if task.DiscussionChannelSlug != nil {
		fmt.Printf("discussion: %s\n", *task.DiscussionChannelSlug)
	}
	if task.Description != nil && strings.TrimSpace(*task.Description) != "" {
		fmt.Printf("\n%s\n", strings.TrimSpace(*task.Description))
	}
	return nil
}

func runAsk(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	model := fs.String("model", "claude-haiku-4-5", "Claude model id")
	noTools := fs.Bool("no-tools", false, "answer without workspace tools (chat only)")
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

	// Answer streams to stdout; tool traces and usage go to stderr so
	// pipelines stay clean.
	exec := &assist.Executor{Client: client, UserID: cfg.UserID}
	opts := assist.LoopOpts{
		Model:  *model,
		System: assist.SystemPrompt(cfg.WorkspaceName, cfg.UserName, !*noTools),
		OnText: func(delta string) { fmt.Print(delta) },
		OnTool: func(name string, input map[string]any) {
			args, _ := json.Marshal(input)
			fmt.Fprintf(os.Stderr, "◇ %s %s\n", name, args)
		},
	}
	if !*noTools {
		opts.Tools = assist.Tools()
	}
	_, res, err := assist.RunLoop(ctx, client, exec,
		[]api.InferenceMessage{api.TextMessage("user", question)}, opts)
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
