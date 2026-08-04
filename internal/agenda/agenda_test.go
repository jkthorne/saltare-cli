package agenda

import (
	"testing"
	"time"

	"github.com/jkthorne/saltare/cli/internal/api"
)

func str(s string) *string { return &s }

func task(id int64, state string, due, dueTime *string) api.Task {
	return api.Task{ID: id, Slug: "t", Title: "T", State: state, DueDate: due, DueTime: dueTime}
}

// A Tuesday, so weekday labels are predictable.
var today = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func TestGroupBucketsAndLabels(t *testing.T) {
	sections := Group([]api.Task{
		task(1, "open", str("2026-08-01"), nil),        // overdue
		task(2, "in_progress", str("2026-08-04"), nil), // today
		task(3, "open", str("2026-08-05"), nil),        // tomorrow
		task(4, "waiting", str("2026-08-07"), nil),     // friday
	}, today, WindowDays)

	labels := make([]string, len(sections))
	for i, s := range sections {
		labels[i] = s.Label
	}
	want := []string{"overdue", "today", "tomorrow", "friday"}
	if len(labels) != len(want) {
		t.Fatalf("got sections %v", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("section %d = %q, want %q (all: %v)", i, labels[i], want[i], labels)
		}
	}
	if sections[0].Date != "" || sections[1].Date != "2026-08-04" {
		t.Fatalf("dates wrong: %q %q", sections[0].Date, sections[1].Date)
	}
}

func TestGroupDropsDoneUndatedAndOutOfWindow(t *testing.T) {
	sections := Group([]api.Task{
		task(1, "completed", str("2026-08-04"), nil),
		task(2, "cancelled", str("2026-08-04"), nil),
		task(3, "open", nil, nil),
		task(4, "open", str(""), nil),
		task(5, "open", str("2026-09-20"), nil), // beyond the window (old-server defense)
	}, today, WindowDays)
	if sections != nil {
		t.Fatalf("expected nil, got %+v", sections)
	}
}

func TestGroupOrdering(t *testing.T) {
	sections := Group([]api.Task{
		task(9, "open", str("2026-08-03"), nil), // overdue, newer date
		task(8, "open", str("2026-08-01"), nil), // overdue, older date
		task(3, "open", str("2026-08-04"), nil), // today, no time — sorts last
		task(2, "open", str("2026-08-04"), str("14:00")),
		task(1, "open", str("2026-08-04"), str("09:00")),
	}, today, WindowDays)

	if len(sections) != 2 {
		t.Fatalf("expected overdue+today, got %d", len(sections))
	}
	if sections[0].Tasks[0].ID != 8 || sections[0].Tasks[1].ID != 9 {
		t.Fatalf("overdue must sort by due date: %+v", ids(sections[0]))
	}
	if got := ids(sections[1]); got != [3]int64{1, 2, 3} {
		t.Fatalf("today must sort by due time with untimed last: %v", got)
	}
}

func ids(s Section) [3]int64 {
	var out [3]int64
	for i, t := range s.Tasks {
		if i < 3 {
			out[i] = t.ID
		}
	}
	return out
}

func TestGroupEmptyInput(t *testing.T) {
	if got := Group(nil, today, WindowDays); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestDueBefore(t *testing.T) {
	if got := DueBefore(today, 7); got != "2026-08-10" {
		t.Fatalf("inclusive upper bound must be today+6, got %s", got)
	}
	if got := DueBefore(today, 1); got != "2026-08-04" {
		t.Fatalf("a 1-day window is just today, got %s", got)
	}
}
