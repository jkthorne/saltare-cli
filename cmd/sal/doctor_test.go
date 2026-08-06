package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestUntilFormatsRemainingLife(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zero value", time.Time{}, "expiry unknown"},
		{"past", time.Now().Add(-time.Hour), "EXPIRED"},
		// Offsets sit off the boundary on purpose: remaining life truncates
		// down, so an exact 72h fixture races the clock into "2d".
		{"hours", time.Now().Add(5*time.Hour + time.Minute), "expires in 5h"},
		{"days", time.Now().Add(72*time.Hour + time.Minute), "expires in 3d"},
	}
	for _, c := range cases {
		if got := until(c.in); got != c.want {
			t.Errorf("%s: until() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExpiredIgnoresZeroValue(t *testing.T) {
	// A missing expiry is unknown, not expired — treating it as expired would
	// fail doctor on configs written before expiries were recorded.
	if expired(time.Time{}) {
		t.Error("zero time must not count as expired")
	}
	if !expired(time.Now().Add(-time.Minute)) {
		t.Error("past time must count as expired")
	}
	if expired(time.Now().Add(time.Hour)) {
		t.Error("future time must not count as expired")
	}
}

func TestDoctorFailCountsAndSummarizes(t *testing.T) {
	var buf bytes.Buffer
	d := &doctor{out: &buf}
	d.ok("config", "fine")
	if err := d.summary(); err != nil {
		t.Fatalf("all-ok must not error, got %v", err)
	}
	d.fail("session", "dead")
	err := d.summary()
	if err == nil || !strings.Contains(err.Error(), "1 check") {
		t.Fatalf("want a 1-failure summary error, got %v", err)
	}
}

func TestDoctorLineIndentsContinuationsUnderTheColumn(t *testing.T) {
	var buf bytes.Buffer
	d := &doctor{out: &buf}
	// Errors from authError arrive pre-indented for the "sal: " prefix; the
	// column layout must re-indent them rather than stack both.
	d.fail("session", "first line\n     second line")
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), buf.String())
	}
	if !strings.HasSuffix(lines[0], "first line") || !strings.HasSuffix(lines[1], "second line") {
		t.Fatalf("unexpected content:\n%s", buf.String())
	}
	if prefix := strings.Index(lines[0], "first"); prefix != strings.Index(lines[1], "second") {
		t.Fatalf("continuation not aligned to the detail column:\n%s", buf.String())
	}
}
