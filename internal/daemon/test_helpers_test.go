package daemon

import (
	"strings"
	"testing"
)

// assertLogContains checks that at least one log line contains all specified substrings.
// Fails with a clear diff showing all log lines if no match is found.
func assertLogContains(t *testing.T, logs []string, substrings ...string) {
	t.Helper()
	for _, line := range logs {
		allMatch := true
		for _, sub := range substrings {
			if sub != "" && !strings.Contains(line, sub) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return
		}
	}
	t.Errorf("no log line contains all of %v; logs:\n%s", substrings, strings.Join(logs, "\n"))
}
