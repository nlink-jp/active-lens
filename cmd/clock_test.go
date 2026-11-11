package cmd

import "time"

// The tests run against fixtures dated in 2026. The read is held to "now", so
// with the wall clock they would pass or fail by the date they were run on.
// Tests that need a particular now set it and restore this one.
func init() {
	clockNow = func() time.Time { return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC) }
}
