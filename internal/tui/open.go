package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tder311/agent-tui/internal/config"
)

// openInTerminal opens a NEW terminal window/tab at dir and runs toolCmd
// (e.g. "claude" or "opencode -s ses_123"). toolCmd may be empty to just
// open a shell. macOS only, via osascript; returns a descriptive error when
// the terminal app can't be driven.
func openInTerminal(termApp, dir, toolCmd string) error {
	app := resolveTerminalApp(termApp)

	shellCmd := "cd " + shellQuote(dir)
	if toolCmd != "" {
		shellCmd += " && " + toolCmd
	}

	var script string
	switch app {
	case config.TerminalITerm:
		script = fmt.Sprintf(`tell application "iTerm2"
	create window with default profile command "%s"
	activate
end tell`, appleScriptEscape(shellCmd))
	default:
		script = fmt.Sprintf(`tell application "Terminal"
	do script "%s"
	activate
end tell`, appleScriptEscape(shellCmd))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("osascript: %s", detail)
		}
		return fmt.Errorf("osascript: %w", err)
	}
	return nil
}

// openInBrowser opens url in the default browser via the macOS `open` command.
func openInBrowser(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "open", url)
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("open: %s", detail)
		}
		return fmt.Errorf("open: %w", err)
	}
	return nil
}

// openBrowserFn opens a URL in the browser; overridable in tests.
var openBrowserFn = openInBrowser

// resolveTerminalApp turns "auto" into a concrete app based on what's installed.
func resolveTerminalApp(termApp string) config.TerminalApp {
	switch config.TerminalApp(termApp) {
	case config.TerminalITerm:
		return config.TerminalITerm
	case config.TerminalTerminal:
		return config.TerminalTerminal
	}
	// auto: prefer iTerm2 when installed, fall back to Terminal.app
	if _, err := os.Stat("/Applications/iTerm.app"); err == nil {
		return config.TerminalITerm
	}
	return config.TerminalTerminal
}

// shellQuote single-quotes a string for POSIX sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appleScriptEscape escapes a string for embedding in an AppleScript "..." literal.
func appleScriptEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
