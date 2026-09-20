package tui

import "time"

// timeout bounds how long the test harness waits for a command; anything
// slower is a tea.Tick and is dropped.
func timeout() <-chan time.Time { return time.After(200 * time.Millisecond) }
