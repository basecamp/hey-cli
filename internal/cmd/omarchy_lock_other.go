//go:build !unix

package cmd

// Omarchy is Linux; elsewhere there is no second bar instance to race.
func withOmarchyPollLock(fn func()) { fn() }
