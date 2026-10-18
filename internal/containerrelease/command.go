package containerrelease

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func execCommand(input string, args ...string) (commandResult, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := commandResult{stdout: strings.TrimSpace(stdout.String()), stderr: strings.TrimSpace(stderr.String())}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.code = exit.ExitCode()
		return result, nil
	}
	return result, err
}

func (a app) run(input string, args ...string) (string, error) {
	result, err := a.exec(input, args...)
	if err != nil {
		return "", fmt.Errorf("run %s: %w", strings.Join(args, " "), err)
	}
	if result.code != 0 {
		return "", fmt.Errorf("command failed (%d): %s\n%s", result.code, strings.Join(args, " "), result.stderr)
	}
	return result.stdout, nil
}
