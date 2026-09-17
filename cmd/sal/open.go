package main

import (
	"flag"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/jkthorne/saltare-cli/internal/config"
	"github.com/jkthorne/saltare-cli/internal/weblink"
)

func runOpen(args []string) error {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	print := fs.Bool("print", false, "print the URL instead of opening it")

	// The kind and slug lead, so `--print` can trail the way every other sal
	// command accepts it.
	positional := []string{}
	for len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		positional = append(positional, args[0])
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	positional = append(positional, fs.Args()...)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	links := weblink.Links{Server: cfg.ServerURL, Workspace: cfg.WorkspaceSlug}

	url, err := resolveOpen(links, positional)
	if err != nil {
		return err
	}

	if *print {
		fmt.Println(url)
		return nil
	}
	return openURL(url)
}

// resolveOpen turns `sal open <kind> <slug>` into a URL. It is separate from
// the launching so the mapping can be tested without a desktop.
func resolveOpen(links weblink.Links, args []string) (string, error) {
	if !links.OK() {
		return "", fmt.Errorf("not configured; run `sal login`")
	}
	if len(args) == 0 {
		return links.Base(), nil
	}

	kind := args[0]
	slug := ""
	if len(args) > 1 {
		slug = args[1]
	}
	if slug == "" {
		return "", fmt.Errorf("usage: sal open %s SLUG", kind)
	}

	switch kind {
	case "channel", "dm", "thread":
		return links.ChannelBySlug(slug), nil
	case "task":
		return links.Task(slug), nil
	case "agent":
		return links.Agent(slug), nil
	case "message":
		if len(args) < 3 {
			return "", fmt.Errorf("usage: sal open message CHANNEL ID")
		}
		id, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil {
			return "", fmt.Errorf("message id must be a number, got %q", args[2])
		}
		kind := ""
		if len(slug) >= len(weblink.ThreadSlugPrefix) && slug[:len(weblink.ThreadSlugPrefix)] == weblink.ThreadSlugPrefix {
			kind = "thread"
		}
		return links.Message(kind, slug, id), nil
	default:
		return "", fmt.Errorf("sal open: unknown kind %q (channel, task, agent, message)", kind)
	}
}

// openURL hands the URL to the desktop and does not wait. A notification's
// --exec runs this; blocking there would hold a process open for as long as
// the browser lives.
func openURL(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		// Printing is the honest fallback: on a headless box the URL is still
		// the answer, it just needs a human to carry it.
		fmt.Println(url)
		return nil
	}
	// Start and return: sal exits immediately and the browser reparents to
	// init. Waiting would hold this process open for as long as the browser
	// lives, which for a notification's --exec is the rest of the day.
	return exec.Command(path, url).Start()
}
