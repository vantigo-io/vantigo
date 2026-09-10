package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// perKey caps how many exchanges are kept per operation and status.
const perKey = 3

// runCorpus deduplicates the raw recordings into one committed JSONL file
// per module: at most perKey exchanges per (operation, status), 5xx dropped,
// exchanges on dropped operations discarded, and requests to routes that do
// not exist (matching no operation, answered 4xx — the suites probe unknown
// paths and API versions) discarded. Any other exchange that matches no
// operation is an error: a path the contract lost.
func runCorpus(args []string) error {
	fs := flag.NewFlagSet("corpus", flag.ContinueOnError)
	in := fs.String("in", "", "directory of raw *.jsonl recordings")
	out := fs.String("out", "", "openapi/testdata/exchanges")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	routersByModule := map[string]routers.Router{}
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(ctx, name)
		if err != nil {
			return err
		}
		r, err := gorillamux.NewRouter(doc)
		if err != nil {
			return err
		}
		routersByModule[name] = r
	}

	raw, err := filepath.Glob(filepath.Join(*in, "*.jsonl"))
	if err != nil {
		return err
	}
	kept := map[string][]string{} // module -> lines
	counts := map[string]int{}    // module|operationId|status -> kept
	var unmatched []string
	for _, file := range raw {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<22)
		for scanner.Scan() {
			line := scanner.Text()
			var ex openapi.Exchange
			if err := json.Unmarshal([]byte(line), &ex); err != nil {
				_ = f.Close()
				return fmt.Errorf("%s: %w", file, err)
			}
			if ex.Status >= 500 || isDropped(ex.Path) {
				continue
			}
			rejected := ex.Status >= 400 && ex.Status < 500
			match := modulePath.FindStringSubmatch(ex.Path)
			if match == nil || routersByModule[match[1]] == nil {
				if !rejected {
					unmatched = append(unmatched, ex.Method+" "+ex.Path)
				}
				continue
			}
			route, _, err := routersByModule[match[1]].FindRoute(httptest.NewRequest(ex.Method, ex.Path, nil))
			if err != nil {
				if !rejected {
					unmatched = append(unmatched, ex.Method+" "+ex.Path)
				}
				continue
			}
			key := fmt.Sprintf("%s|%s|%d", match[1], route.Operation.OperationID, ex.Status)
			if counts[key] >= perKey {
				continue
			}
			counts[key]++
			kept[match[1]] = append(kept[match[1]], line)
		}
		_ = f.Close()
		if err := scanner.Err(); err != nil {
			return err
		}
	}
	if len(unmatched) > 0 {
		sort.Strings(unmatched)
		return fmt.Errorf("%d recorded exchanges match no contract operation, e.g. %s", len(unmatched), strings.Join(unmatched[:min(5, len(unmatched))], "; "))
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	for _, name := range openapi.Modules {
		lines := kept[name]
		sort.Strings(lines) // deterministic output
		if err := os.WriteFile(filepath.Join(*out, name+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// runCoverage writes the operations that no recorded exchange exercises.
func runCoverage(args []string) error {
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	corpus := fs.String("corpus", "", "openapi/testdata/exchanges")
	out := fs.String("out", "", "openapi/COVERAGE.md")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	var b strings.Builder
	b.WriteString("# Contract coverage\n\nOperations no recorded .NET exchange exercises. Their contract comes from the endpoint code alone; review them by hand. Regenerate with `go run ./internal/openapi/cmd/contract coverage` from apps/server.\n")
	total, uncovered := 0, 0
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(ctx, name)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		r, err := gorillamux.NewRouter(doc)
		if err != nil {
			return err
		}
		data, _ := os.ReadFile(filepath.Join(*corpus, name+".jsonl"))
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var ex openapi.Exchange
			if line == "" || json.Unmarshal([]byte(line), &ex) != nil {
				continue
			}
			if route, _, err := r.FindRoute(httptest.NewRequest(ex.Method, ex.Path, nil)); err == nil {
				seen[route.Operation.OperationID] = true
			}
		}
		var missing []string
		for p, item := range doc.Paths.Map() {
			for method, op := range item.Operations() {
				total++
				if !seen[op.OperationID] {
					missing = append(missing, fmt.Sprintf("- `%s %s` (%s)", method, p, op.OperationID))
				}
			}
		}
		sort.Strings(missing)
		uncovered += len(missing)
		fmt.Fprintf(&b, "\n## %s (%d uncovered)\n\n%s\n", name, len(missing), strings.Join(missing, "\n"))
	}
	fmt.Fprintf(&b, "\nTotal: %d of %d operations have no recorded exchange.\n", uncovered, total)
	return os.WriteFile(*out, []byte(b.String()), 0o644)
}
