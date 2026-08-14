package ui

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// openURL opens a URL in the user's default browser.
//
// The URL is parsed and scheme-checked before it reaches a command line. It
// comes from the Coolify API rather than from the user, but an application's
// fqdn field is still remote input, and passing it unchecked to a shell-adjacent
// command would be an injection route.
func openURL(raw string) error {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("refusing to open a %q URL", target.Scheme)
	}
	if target.Host == "" {
		return fmt.Errorf("URL has no host")
	}

	name, args := browserCommand(target.String())
	if name == "" {
		return errNoBrowser
	}
	// exec.Command does not go through a shell, so the URL is passed as a single
	// argv entry and cannot be interpreted as further arguments or commands.
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the child rather than leaving a zombie; the opener exits immediately.
	go func() { _ = cmd.Wait() }()
	return nil
}

// browserCommand returns the platform's URL opener.
func browserCommand(target string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{target}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		return "xdg-open", []string{target}
	}
}
