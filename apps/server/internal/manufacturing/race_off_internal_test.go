//go:build !race

package manufacturing

// raceDetector reports a build with the race detector, which slows the
// timed tests by an order of magnitude.
const raceDetector = false
