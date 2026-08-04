// Package agenda groups tasks into day buckets for the TUI agenda view and
// the `sal agenda` subcommand. Dates are compared as "2006-01-02" strings —
// the serializer's date-only fields are not RFC3339 (see api.Task).
package agenda

import (
	"sort"
	"strings"
	"time"

	"github.com/jkthorne/saltare/cli/internal/api"
)

// WindowDays is the default agenda span, today inclusive.
const WindowDays = 7

// Section is one bucket of the agenda: "overdue" or a single day.
type Section struct {
	Label string     `json:"label"` // "overdue", "today", "tomorrow", "wednesday", …
	Date  string     `json:"date"`  // "2006-01-02"; "" for overdue
	Tasks []api.Task `json:"tasks"`
}

// DueBefore returns the inclusive upper bound for a days-wide window
// starting at today.
func DueBefore(today time.Time, days int) string {
	return today.AddDate(0, 0, days-1).Format("2006-01-02")
}

// Group buckets tasks into overdue + one section per day, today..today+days-1.
// It drops completed/cancelled tasks, tasks with no due date, and dates
// outside the window (old servers ignore the due_* filters and return
// everything). Empty sections are omitted.
func Group(tasks []api.Task, today time.Time, days int) []Section {
	todayStr := today.Format("2006-01-02")

	byDate := make(map[string][]api.Task)
	var overdue []api.Task
	for _, t := range tasks {
		if t.State == "completed" || t.State == "cancelled" {
			continue
		}
		if t.DueDate == nil || *t.DueDate == "" {
			continue
		}
		if *t.DueDate < todayStr {
			overdue = append(overdue, t)
			continue
		}
		byDate[*t.DueDate] = append(byDate[*t.DueDate], t)
	}

	var sections []Section
	if len(overdue) > 0 {
		sortSection(overdue)
		sections = append(sections, Section{Label: "overdue", Tasks: overdue})
	}
	for i := 0; i < days; i++ {
		day := today.AddDate(0, 0, i)
		date := day.Format("2006-01-02")
		bucket := byDate[date]
		if len(bucket) == 0 {
			continue
		}
		sortSection(bucket)
		sections = append(sections, Section{Label: dayLabel(day, i), Date: date, Tasks: bucket})
	}
	return sections
}

func dayLabel(day time.Time, offset int) string {
	switch offset {
	case 0:
		return "today"
	case 1:
		return "tomorrow"
	default:
		return strings.ToLower(day.Weekday().String())
	}
}

// sortSection orders overdue by due date, then within any bucket by due time
// (untimed last), then ID — deterministic output for tests and --json.
func sortSection(tasks []api.Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if ad, bd := deref(a.DueDate), deref(b.DueDate); ad != bd {
			return ad < bd
		}
		at, bt := deref(a.DueTime), deref(b.DueTime)
		if at != bt {
			if at == "" {
				return false
			}
			if bt == "" {
				return true
			}
			return at < bt
		}
		return a.ID < b.ID
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
