package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jkthorne/saltare-cli/internal/agenda"
	"github.com/jkthorne/saltare-cli/internal/api"
)

// runAgenda prints your next N days of tasks grouped by day — the scriptable
// twin of the TUI's agenda mode (both share internal/agenda).
func runAgenda(args []string) error {
	fs := flag.NewFlagSet("agenda", flag.ExitOnError)
	days := fs.Int("days", agenda.WindowDays, "window size in days, today inclusive")
	asJSON := fs.Bool("json", false, "print raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *days < 1 {
		return fmt.Errorf("--days must be at least 1")
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	today := time.Now()
	tasks, err := client.Tasks(ctx, api.TasksOpts{Mine: true, DueBefore: agenda.DueBefore(today, *days)})
	if err != nil {
		return err
	}
	sections := agenda.Group(tasks, today, *days)
	if *asJSON {
		return printJSON(sections)
	}
	if len(sections) == 0 {
		fmt.Fprintf(os.Stderr, "nothing due in the next %d days\n", *days)
		return nil
	}
	for i, s := range sections {
		if i > 0 {
			fmt.Println()
		}
		label := s.Label
		if s.Date != "" {
			label += " · " + s.Date
		}
		fmt.Println(label)
		for _, t := range s.Tasks {
			when := ""
			if s.Date == "" && t.DueDate != nil {
				when = "  was due " + *t.DueDate
			} else if t.DueTime != nil && *t.DueTime != "" {
				when = "  at " + *t.DueTime
			}
			fmt.Printf("  %s %-24s %s%s\n", stateGlyph(t.State), t.Slug, t.Title, when)
		}
	}
	return nil
}

func stateGlyph(state string) string {
	switch state {
	case "open":
		return "○"
	case "in_progress":
		return "◐"
	case "waiting":
		return "◇"
	default:
		return "·"
	}
}
