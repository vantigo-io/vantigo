package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// A corpus file that exists but cannot be read is a broken checkout, not an
// unrecorded module: runCoverage must stop rather than report every
// operation of that module uncovered. The unreadable file here is a
// directory where a .jsonl is expected, which os.ReadFile refuses with
// something other than os.ErrNotExist — the one error the missing-file case
// below is allowed to swallow.
func TestCoverageFailsOnAnUnreadableCorpus(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, openapi.Modules[0]+".jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := runCoverage([]string{"-corpus", dir, "-out", filepath.Join(dir, "COVERAGE.md")})
	if err == nil {
		t.Fatal("runCoverage ignored a corpus it could not read")
	}
}

// A module with no corpus file has no recorded exchanges, which is a report
// of an entirely uncovered module rather than a failure — every module born
// in this repository, projects first, is in that position permanently.
func TestCoverageTreatsAMissingCorpusFileAsNoExchanges(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "COVERAGE.md")
	if err := runCoverage([]string{"-corpus", dir, "-out", out}); err != nil {
		t.Fatalf("runCoverage on an empty corpus directory: %v", err)
	}
	report, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "- `POST /api/v1/projects` (postProjects)") {
		t.Errorf("report does not list projects' own operations as uncovered:\n%s", report)
	}
}

func TestSampleKeySeparatesShapesUnderOneStatus(t *testing.T) {
	str := func(s string) *string { return &s }
	ex := func(contentType, body *string) openapi.Exchange {
		return openapi.Exchange{Method: "POST", Path: "/api/v1/customers", Status: 400, ResponseContentType: contentType, ResponseBody: body}
	}
	validation := sampleKey("customers", "postCustomers", ex(str("application/problem+json"), str(`{"type":"t","title":"x","status":400,"errors":{}}`)))
	validationCharset := sampleKey("customers", "postCustomers", ex(str("application/problem+json; charset=utf-8"), str(`{"errors":{},"status":400,"title":"y","type":"u"}`)))
	antiforgery := sampleKey("customers", "postCustomers", ex(str("application/json; charset=utf-8"), str(`{"error":{"code":"csrf_validation_failed","message":"m","fields":null}}`)))
	empty := sampleKey("customers", "postCustomers", ex(nil, nil))
	array := sampleKey("customers", "postCustomers", ex(str("application/json"), str(`[{"a":1}]`)))

	if validation != validationCharset {
		t.Errorf("same shape, different values or charset: %q != %q", validation, validationCharset)
	}
	for _, other := range []string{antiforgery, empty, array} {
		if other == validation {
			t.Errorf("distinct shape %q collides with %q", other, validation)
		}
	}
	if want := "customers|postCustomers|400|application/json|{error}"; antiforgery != want {
		t.Errorf("antiforgery key = %q, want %q", antiforgery, want)
	}
	if want := "customers|postCustomers|400|-|-"; empty != want {
		t.Errorf("empty key = %q, want %q", empty, want)
	}
}
