// Command contract maintains the Go port's OpenAPI contract files:
//
//	contract split     -in <dump.json> -out <dir>    split the .NET dump into per-module files
//	contract normalize -dir <dir>                    type numeric schemas and rewrite nullable references in place
//	contract corpus    -in <dir> -out <dir>          validate every raw recorded exchange, then write a per-module sample
//	contract coverage  -corpus <dir> -out <file>     list the operations no sampled exchange exercises
//
// Run from apps/server, e.g.
//
//	go run ./internal/openapi/cmd/contract corpus -in /tmp/vantigo-exchanges -out ../../openapi/testdata/exchanges
//	go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
//	go run ./internal/openapi/cmd/contract normalize -dir ../../openapi
//
// See docs/superpowers/plans/2026-09-10-api-contract.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/oasdiff/yaml"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: contract <split|normalize|corpus|coverage> [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "split":
		err = runSplit(os.Args[2:])
	case "normalize":
		err = runNormalize(os.Args[2:])
	case "corpus":
		err = runCorpus(os.Args[2:])
	case "coverage":
		err = runCoverage(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runSplit(args []string) error {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	in := fs.String("in", "", "the .NET OpenAPI dump (JSON)")
	out := fs.String("out", "", "the directory to write the module files into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	files, err := split(data)
	if err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(*out, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// runNormalize rewrites every *.yaml file in dir in place: it types the
// numeric schemas the 3.1 -> 3.0 downgrade left untyped, drops the
// numeric-string patterns it added to numbers, and rewrites
// nullable single-$ref oneOf schemas into the allOf idiom, using the same
// YAML<->JSON round trip as split so the files stay sorted and 4-space
// indented.
func runNormalize(args []string) error {
	fs := flag.NewFlagSet("normalize", flag.ContinueOnError)
	dir := fs.String("dir", "", "the directory of *.yaml contract files to rewrite in place")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(*dir, "*.yaml"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	var totalNumbers, totalRefs int
	for _, path := range files {
		numbers, refs, err := normalizeFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		totalNumbers += numbers
		totalRefs += refs
		fmt.Printf("%s: %d numeric schema(s) typed or stripped of a numeric-string pattern, %d nullable reference(s) rewritten\n", filepath.Base(path), numbers, refs)
	}
	fmt.Printf("total: %d numeric schema(s) typed or stripped of a numeric-string pattern, %d nullable reference(s) rewritten\n", totalNumbers, totalRefs)
	return nil
}

func normalizeFile(path string) (numbers, refs int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	j, err := yaml.YAMLToJSON(data)
	if err != nil {
		return 0, 0, err
	}
	var doc obj
	if err := json.Unmarshal(j, &doc); err != nil {
		return 0, 0, err
	}
	numbers = normalizeNumbers(doc)
	refs = normalizeNullableRefs(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		return 0, 0, err
	}
	y, err := yaml.JSONToYAML(out)
	if err != nil {
		return 0, 0, err
	}
	return numbers, refs, os.WriteFile(path, y, 0o644)
}
