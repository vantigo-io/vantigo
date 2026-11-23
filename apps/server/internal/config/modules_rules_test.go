package config

import (
	"slices"
	"testing"
)

// TestModuleRules: every rule names known modules, and ModuleRules answers
// every known module once, so `vantigo modules --json` lists what MODULES
// accepts and what it refuses.
func TestModuleRules(t *testing.T) {
	for _, r := range moduleRequires {
		if !slices.Contains(knownModules, r.module) {
			t.Errorf("rule for unknown module %q", r.module)
		}
		for _, needed := range r.requires {
			if !slices.Contains(knownModules, needed) {
				t.Errorf("%s requires unknown module %q", r.module, needed)
			}
		}
	}
	rules := ModuleRules()
	if len(rules) != len(knownModules) {
		t.Fatalf("%d rules, want one per known module (%d)", len(rules), len(knownModules))
	}
	for i, r := range rules {
		if r.Key != knownModules[i] {
			t.Errorf("rule %d is %q, want %q", i, r.Key, knownModules[i])
		}
		p := &problems{}
		modules(p, map[string]string{"MODULES": r.Key})
		if (len(*p) == 0) != (len(r.Requires) == 0) {
			t.Errorf("MODULES=%s alone: problems %v, but it requires %v", r.Key, *p, r.Requires)
		}
	}
}
