package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const serviceUnit = "saltare-watch.service"

// unitTemplate keeps Restart=on-failure rather than always on purpose: a
// logged-out `sal watch` publishes its state and exits 0, and the bar's "sign
// in" instruction is only reachable if the supervisor lets it stay exited.
const unitTemplate = `[Unit]
Description=Saltare workspace watcher
Documentation=https://github.com/jkthorne/saltare-cli
After=graphical-session.target
PartOf=graphical-session.target

[Service]
Type=simple
ExecStart=%s watch --notify
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=graphical-session.target
`

// installService writes the user unit and starts it.
//
// This lives in the CLI rather than in the Omarchy plugin because the plugin
// installer deliberately "never runs plugin code, install hooks, or sudo" — a
// plugin cannot set itself up. `sal` can, and the user was already running
// `sal login`, so the whole setup story stays two commands.
func installService() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("--install-service is systemd, so Linux only; on macOS run `sal watch --notify` from your login items")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl is not on PATH — run `sal watch --notify` from whatever supervises your session")
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	dir, err := unitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, serviceUnit)
	if err := os.WriteFile(path, []byte(fmt.Sprintf(unitTemplate, self)), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote " + path)

	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", serviceUnit},
	} {
		out, err := exec.Command("systemctl", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	fmt.Println("enabled and started " + serviceUnit)
	fmt.Println("check it with: systemctl --user status " + serviceUnit)
	return nil
}

func uninstallService() error {
	dir, err := unitDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, serviceUnit)

	if _, err := exec.LookPath("systemctl"); err == nil {
		// Best effort: a unit that was never enabled still needs its file gone,
		// and failing here would leave the file behind for no good reason.
		_ = exec.Command("systemctl", "--user", "disable", "--now", serviceUnit).Run()
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	fmt.Println("removed " + path)
	return nil
}

func unitDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}
