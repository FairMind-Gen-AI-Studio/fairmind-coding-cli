package auth

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Service is the credential-store service name used for FairMind tokens.
const Service = "fairmind-cli"

// SystemKeychain uses the OS credential store through its standard CLI:
// macOS `security`, Linux `secret-tool` (libsecret). The token is always
// passed on stdin, never as a process argument.
type SystemKeychain struct{}

var errUnsupported = errors.New("no supported OS credential store on this platform; use " + EnvVar)

func (SystemKeychain) Get(profile string) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("security", "find-generic-password", "-s", Service, "-a", profile, "-w")
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return "", ErrNotFound
		}
		cmd = exec.Command("secret-tool", "lookup", "service", Service, "account", profile)
	default:
		return "", ErrNotFound
	}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Both tools exit non-zero when the item does not exist.
			return "", ErrNotFound
		}
		return "", fmt.Errorf("credential store lookup failed: %w", err)
	}
	v := strings.TrimSpace(out.String())
	if v == "" {
		return "", ErrNotFound
	}
	return v, nil
}

func (k SystemKeychain) Set(profile, token string) error {
	if strings.ContainsAny(token, "\"'\\ \t\r\n") {
		return errors.New("token contains unexpected characters")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// `security -i` reads commands from stdin, keeping the token out of argv.
		cmd = exec.Command("security", "-i")
		cmd.Stdin = strings.NewReader(fmt.Sprintf(
			"add-generic-password -U -s %s -a %q -l %q -w \"%s\"\n", Service, profile, "FairMind CLI ("+profile+")", token))
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return errUnsupported
		}
		cmd = exec.Command("secret-tool", "store", "--label=FairMind CLI ("+profile+")", "service", Service, "account", profile)
		cmd.Stdin = strings.NewReader(token)
	default:
		return errUnsupported
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("credential store write failed: %w", err)
	}
	got, err := k.Get(profile)
	if err != nil || got != token {
		return errors.New("credential store write could not be verified")
	}
	return nil
}

func (SystemKeychain) Delete(profile string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("security", "delete-generic-password", "-s", Service, "-a", profile)
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return ErrNotFound
		}
		cmd = exec.Command("secret-tool", "clear", "service", Service, "account", profile)
	default:
		return errUnsupported
	}
	if err := cmd.Run(); err != nil {
		return ErrNotFound
	}
	return nil
}
