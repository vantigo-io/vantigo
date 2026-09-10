// Command contract maintains the Go port's OpenAPI contract files:
//
//	contract split    -in <dump.json> -out <dir>     split the .NET dump into per-module files
//	contract corpus   -in <dir> -spec <dir> -out <dir>  deduplicate recorded exchanges (Task 4)
//	contract coverage -spec <dir> -corpus <dir> -out <file>  list operations without exchanges (Task 4)
//
// Run from apps/server. See docs/superpowers/plans/2026-09-10-api-contract.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: contract <split|corpus|coverage> [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "split":
		err = runSplit(os.Args[2:])
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
