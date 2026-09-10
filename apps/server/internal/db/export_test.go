package db

import "time"

// MigrationsFS exposes the embedded migrations to the external test package.
var MigrationsFS = migrationsFS

// SetRetryDelay replaces the connect back-off for a test and returns a restore func.
func SetRetryDelay(f func(attempt int) time.Duration) (restore func()) {
	previous := retryDelay
	retryDelay = f
	return func() { retryDelay = previous }
}
