package accounting

import "time"

// SetMaxReportLines lowers the reports' cap for a test and gives back the
// function that restores it.
func SetMaxReportLines(n int) (restore func()) {
	was := maxReportLines
	maxReportLines = n
	return func() { maxReportLines = was }
}

// SetCatchUpLease sets the lease a catch-up claim takes for a test and gives
// back the function that restores it.
func SetCatchUpLease(d time.Duration) (restore func()) {
	was := catchupLease
	catchupLease = d
	return func() { catchupLease = was }
}

// SetSaftLease sets the lease a SAF-T export's claim takes for a test and
// gives back the function that restores it.
func SetSaftLease(d time.Duration) (restore func()) {
	was := saftLease
	saftLease = d
	return func() { saftLease = was }
}
