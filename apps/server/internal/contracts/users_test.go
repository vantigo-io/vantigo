package contracts_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestIncludeAgents_DefaultsToFalse proves a search or a holders list leaves
// agents out unless the caller asks (virtual employees design V5).
func TestIncludeAgents_DefaultsToFalse(t *testing.T) {
	t.Parallel()
	if contracts.ResolveDirectoryOptions(nil).IncludeAgents {
		t.Error("no option includes agents")
	}
	if !contracts.ResolveDirectoryOptions([]contracts.DirectoryOption{contracts.IncludeAgents()}).IncludeAgents {
		t.Error("IncludeAgents() does not include agents")
	}
}

// TestIncludeAgents_OnlyTheModulesThatMeanToAskPass pins who passes
// contracts.IncludeAgents(): every other caller of SearchUsers and
// PermissionHolders keeps the default, so a customer's owner, a calendar, a
// project, a rate card or Staff never meets an agent by accident. A module
// that should see agents is added here on purpose.
func TestIncludeAgents_OnlyTheModulesThatMeanToAskPass(t *testing.T) {
	t.Parallel()
	allowed := []string{"contracts", "identity", "communications", "knowledge"}
	root := ".."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(b), "IncludeAgents()") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		module, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		for _, ok := range allowed {
			if module == ok {
				return nil
			}
		}
		t.Errorf("%s passes IncludeAgents(); only %v may", rel, allowed)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
