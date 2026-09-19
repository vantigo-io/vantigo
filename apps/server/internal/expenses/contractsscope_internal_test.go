package expenses

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rule contractscalls.go states is that every reach into another module —
// deps.Projects and deps.Users — goes through one of its accessors and through
// nowhere else. The harness-wide locked-call check enforces the *timing* of
// those calls, but only for calls that go through an accessor: a direct
// s.deps.Projects.Project(ctx, …) inside a locked transaction would be
// invisible to it.
//
// This is the other half, and it is a scan rather than a convention: the
// package's own source is read and the two fields are allowed to appear in
// exactly one file. It is the same shape as internal/db's cross-schema scan —
// cheap, and it fails the moment somebody adds the twenty-first call site in
// the wrong place.
func TestDepsDirectories_AreReachedOnlyThroughTheAccessors(t *testing.T) {
	t.Parallel()
	const accessors = "contractscalls.go"
	// This file names the two fields in order to look for them, so it is the
	// one other file the scan skips.
	const scanner = "contractsscope_internal_test.go"
	fields := []string{"deps." + "Projects", "deps." + "Users"}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	found := map[string][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || name == accessors || name == scanner {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, field := range fields {
			if strings.Contains(string(source), field) {
				found[field] = append(found[field], name)
			}
		}
	}
	for field, files := range found {
		t.Errorf("%s is read in %s; every cross-module call belongs in %s, behind an accessor that "+
			"reports it (noteContractCall), so that the locked-transaction check can see it",
			field, strings.Join(files, ", "), accessors)
	}

	// And the scan is only worth anything if the two names are actually there
	// to be found in the file it allows them in.
	source, err := os.ReadFile(accessors)
	if err != nil {
		t.Fatalf("read %s: %v", accessors, err)
	}
	for _, field := range fields {
		if !strings.Contains(string(source), field) {
			t.Errorf("%s does not mention %s — the scan above would pass whatever the rest of the "+
				"package does", accessors, field)
		}
	}
}
