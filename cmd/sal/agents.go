package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func runAgents(args []string) error {
	if len(args) > 0 && args[0] == "message" {
		return agentMessage(args[1:])
	}

	fs := flag.NewFlagSet("agents", flag.ExitOnError)
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

	agents, err := client.Agents(ctx)
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(agents)
	}
	for _, a := range agents {
		description := ""
		if a.Description != nil {
			description = *a.Description
		}
		fmt.Printf("  %-18s %-10s %s\n", a.Slug, a.Status, description)
	}
	return nil
}

// agentMessage sends to an agent's 1:1 DM, which the server finds-or-creates.
// This is the one thing the TUI could do and the CLI could not, and the
// capture overlay's `@agent …` route needs it from outside a terminal.
func agentMessage(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: sal agents message AGENT [MESSAGE]")
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

	cfg, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	message, channelID, err := client.MessageAgent(ctx, slug, body)
	if err != nil {
		return authError(cfg, err)
	}
	// The agent answers in the background, so the useful thing to print is
	// where the answer will appear rather than the echo of what was sent.
	_ = message
	fmt.Printf("sent to @%s — the reply lands in the DM (channel %d)\n", slug, channelID)
	return nil
}
