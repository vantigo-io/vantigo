// Package buildinfo is the one place the running binary's version lives.
//
// Version is stamped at link time (-X …/buildinfo.Version=<v>) by
// .goreleaser.yaml for releases and by scripts/build-artifacts.sh
// (VANTIGO_VERSION) for CI preview images; a plain `go build` leaves "dev".
// It is the same string the image is tagged with, and everything that names a
// version names this one: the boot log, /health responses and the
// OpenTelemetry resource.
package buildinfo

// Version is the build version, or "dev" when not stamped.
var Version = "dev"
