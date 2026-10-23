// Package githubapi wraps GitHub CLI API requests with HTTP diagnostics while
// preserving machine-readable output on success.
package githubapi

import (
	"regexp"
	"strings"
)

// gh inserts a status line and headers before each response body, including
// inside the outer array produced by --paginate --slurp. Header values cannot
// contain literal newlines; JSON strings escape them. Match only HTTP header
// blocks, leaving JSON bodies and --jq results untouched.
var githubResponseHeaders = regexp.MustCompile("HTTP/[0-9.]+ [0-9]{3}[^\\r\\n]*\\r?\\n(?:[!#$%&'*+.^_`|~0-9A-Za-z-]+:[^\\r\\n]*(?:\\r?\\n|$))*(?:\\r?\\n|$)")

// Runner executes a command with the supplied stdin. On failure, its error must
// include bounded stdout and stderr, without logging stdin or request secrets.
type Runner func(input string, args ...string) (string, error)

// Call captures response status, headers, and body for failure diagnostics
// on every GitHub API request. Successful callers still receive only the body
// (or --jq/--slurp output), never headers. Do not use --verbose or GH_DEBUG=api:
// those also log request credentials and payloads. Requests are never retried,
// since even a failed response may have committed a server-side mutation.
func Call(run Runner, input string, args ...string) (string, error) {
	command := append([]string{"gh", "api"}, args...)
	command = append(command, "--include")
	response, err := run(input, command...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(githubResponseHeaders.ReplaceAllString(response, "")), nil
}
