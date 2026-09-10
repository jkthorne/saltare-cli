package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/config"
)

// runDoctor walks the boot chain in order — config, tokens, server, session,
// identity — and reports each step. Every check short of the network is cheap,
// so it stays useful when the server is the thing that is down.
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	d := &doctor{out: os.Stdout}
	fmt.Fprintln(d.out, "sal doctor")
	fmt.Fprintln(d.out)

	cfg := d.checkConfig()
	if cfg == nil {
		return d.summary()
	}
	d.checkTokens()
	d.checkServer(cfg)
	d.checkSession(cfg)
	return d.summary()
}

type doctor struct {
	out    io.Writer
	failed int
}

func (d *doctor) ok(name, detail string)   { d.line("ok  ", name, detail) }
func (d *doctor) warn(name, detail string) { d.line("warn", name, detail) }

func (d *doctor) fail(name, detail string) {
	d.failed++
	d.line("FAIL", name, detail)
}

// line keeps continuation text under the detail column so multi-line findings
// stay readable next to single-line ones.
func (d *doctor) line(status, name, detail string) {
	parts := strings.Split(detail, "\n")
	fmt.Fprintf(d.out, "  %-4s  %-10s  %s\n", status, name, strings.TrimSpace(parts[0]))
	for _, p := range parts[1:] {
		// Errors shared with the top-level printer carry their own indent for
		// the "sal: " prefix; the column layout supplies it here instead.
		fmt.Fprintf(d.out, "  %-4s  %-10s  %s\n", "", "", strings.TrimSpace(p))
	}
}

func (d *doctor) summary() error {
	fmt.Fprintln(d.out)
	if d.failed == 0 {
		fmt.Fprintln(d.out, "all checks passed")
		return nil
	}
	return fmt.Errorf("%d check(s) failed", d.failed)
}

func (d *doctor) checkConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		d.fail("config", err.Error())
		return nil
	}
	who := cfg.Email
	if who == "" {
		who = "(no email recorded)"
	}
	d.ok("config", fmt.Sprintf("server %s\nworkspace %s · %s", cfg.ServerURL, cfg.WorkspaceSlug, who))
	return cfg
}

func (d *doctor) checkTokens() {
	src := config.TokensSource()
	tokens, err := config.LoadTokens()
	if err != nil {
		d.fail("tokens", err.Error())
		return
	}
	// Never print token material — only where it lives and how long it lasts.
	detail := src + " · access " + until(tokens.AccessExpiresAt) + " · refresh " + until(tokens.RefreshExpiresAt)
	if expired(tokens.RefreshExpiresAt) {
		d.fail("tokens", detail+"\nthe refresh token has expired — run `sal login`")
		return
	}
	d.ok("tokens", detail)
}

func (d *doctor) checkServer(cfg *config.Config) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(cfg.ServerURL)
	if err != nil {
		d.fail("server", fmt.Sprintf("%s unreachable\n%v", cfg.ServerURL, err))
		return
	}
	defer resp.Body.Close()
	d.ok("server", fmt.Sprintf("%s reachable (HTTP %d)", cfg.ServerURL, resp.StatusCode))
}

// checkSession is the one that matters: it proves the stored token still
// authenticates, and compares who the server says we are against what the
// config claims. A reseeded dev database leaves those two disagreeing.
func (d *doctor) checkSession(cfg *config.Config) {
	_, client, err := session()
	if err != nil {
		d.fail("session", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	me, err := client.Me(ctx)
	if err != nil {
		if errors.Is(err, api.ErrAuthExpired) {
			d.fail("session", authError(cfg, err).Error())
			return
		}
		d.fail("session", err.Error())
		return
	}
	d.ok("session", fmt.Sprintf("authenticated as %s <%s> (user %d)", me.User.Name, me.User.Email, me.User.ID))
	d.ok("workspace", fmt.Sprintf("%s (%s) · plan %s", me.Workspace.Name, me.Workspace.Slug, me.Workspace.Plan))

	var drift []string
	if cfg.UserID != 0 && cfg.UserID != me.User.ID {
		drift = append(drift, fmt.Sprintf("config user_id %d, server says %d", cfg.UserID, me.User.ID))
	}
	if cfg.Email != "" && !strings.EqualFold(cfg.Email, me.User.Email) {
		drift = append(drift, fmt.Sprintf("config email %s, server says %s", cfg.Email, me.User.Email))
	}
	if cfg.WorkspaceSlug != "" && cfg.WorkspaceSlug != me.Workspace.Slug {
		drift = append(drift, fmt.Sprintf("config workspace %s, server says %s", cfg.WorkspaceSlug, me.Workspace.Slug))
	}
	if len(drift) > 0 {
		d.warn("identity", strings.Join(append(drift, "run `sal login` to resync"), "\n"))
		return
	}
	d.ok("identity", "config matches the server")
}

func expired(t time.Time) bool { return !t.IsZero() && time.Now().After(t) }

func until(t time.Time) string {
	if t.IsZero() {
		return "expiry unknown"
	}
	dur := time.Until(t)
	if dur <= 0 {
		return "EXPIRED"
	}
	if days := int(dur.Hours() / 24); days > 0 {
		return fmt.Sprintf("expires in %dd", days)
	}
	return fmt.Sprintf("expires in %dh", int(dur.Hours()))
}
