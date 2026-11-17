//go:build e2e

package e2e

// The harness's only waiting primitive. It lives outside the test files because
// the repository keeps a ratchet on fixed waits in tests: a wait belongs to a
// condition, and the polling cadence belongs in one place that can be read and
// changed deliberately.

import (
	"time"
)

// pollInterval is how often a condition is re-checked. It is a cadence, not a
// wait: nothing proceeds on it, and every use fails with what the condition last
// observed rather than assuming the elapsed time made something true.
const pollInterval = 50 * time.Millisecond

// waitFor polls condition until it is true, the process under test stops, or the
// deadline passes. stopped reports the process exit; explain describes the last
// observation so a failure says what was actually there.
func waitFor(description string, timeout time.Duration, condition func() bool, stopped func() bool, explain func() string) (ok bool, reason string) {
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return true, ""
		}
		if stopped() {
			return false, "the service exited before " + description + "\n" + explain()
		}
		if time.Now().After(deadline) {
			return false, "timed out waiting for " + description + "\n" + explain()
		}
		time.Sleep(pollInterval)
	}
}
