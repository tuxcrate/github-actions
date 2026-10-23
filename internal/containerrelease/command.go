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
		diagnostics := "stderr:\n" + commandDiagnostic(result.stderr)
		if result.stdout != "" {
			diagnostics += "\nstdout:\n" + commandDiagnostic(result.stdout)
		}
		return "", fmt.Errorf("command failed (%d): %s\n%s", result.code, strings.Join(args, " "), diagnostics)
	}
	return result.stdout, nil
}

// Bound logged output while retaining the start of HTTP responses, including
// their status, request ID, rate-limit headers, and error body. Never log stdin:
// it can contain a request payload rather than diagnostic information.
func commandDiagnostic(output string) string {
	const limit = 8 * 1024
	if len(output) > limit {
		return output[:limit] + "\n[truncated]"
	}
	if output == "" {
		return "[empty]"
	}
	return output
}
