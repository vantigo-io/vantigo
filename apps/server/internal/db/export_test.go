package db

import "time"

// MigrationsFS exposes the embedded migrations to the external test package.
var MigrationsFS = migrationsFS

// WaitForDatabase exposes the connect retry loop to the external test package.
var WaitForDatabase = waitForDatabase

// SetPingTimeout replaces the per-attempt ping bound for a test and returns a restore func.
func SetPingTimeout(d time.Duration) (restore func()) {
	previous := pingTimeout
	pingTimeout = d
	return func() { pingTimeout = previous }
}

// SetRetryDelay replaces the connect back-off for a test and returns a restore func.
func SetRetryDelay(f func(attempt int) time.Duration) (restore func()) {
	previous := retryDelay
	retryDelay = f
	return func() { retryDelay = previous }
}
