package accounting

// SetMaxReportLines lowers the reports' cap for a test and gives back the
// function that restores it.
func SetMaxReportLines(n int) (restore func()) {
	was := maxReportLines
	maxReportLines = n
	return func() { maxReportLines = was }
}
