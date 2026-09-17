package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// perKey caps how many exchanges are kept per sampling key (see sampleKey).
const perKey = 3

// runCorpus deduplicates the raw recordings into one committed JSONL file
// per module. 5xx exchanges, exchanges on dropped operations, and requests to
// routes that do not exist (matching no operation, answered 4xx — the suites
// probe unknown paths and API versions) are discarded; any other exchange
// that matches no operation is an error: a path the contract lost.
//
// Every remaining raw exchange is validated against the contract before
// anything is written, with the same rules as the committed-corpus test: the
// committed corpus is a sample of a raw set that validated in full. If any
// exchange fails, runCorpus prints every failure and writes nothing.
//
// The sample keeps at most perKey exchanges per sampling key, so every
// response shape the suites recorded — not just every status — survives.
func runCorpus(args []string) error {
	fs := flag.NewFlagSet("corpus", flag.ContinueOnError)
	in := fs.String("in", "", "directory of raw *.jsonl recordings")
	out := fs.String("out", "", "openapi/testdata/exchanges")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	docs := map[string]*openapi3.T{}
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
		docs[name], routersByModule[name] = doc, r
	}

	raw, err := filepath.Glob(filepath.Join(*in, "*.jsonl"))
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return fmt.Errorf("no raw recordings (*.jsonl) in %q", *in)
	}
	kept := map[string][]string{} // module -> lines
	counts := map[string]int{}    // sampling key -> kept
	failures := map[string]*failure{}
	var unmatched []string
	validated := 0
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
			module := match[1]
			route, _, err := routersByModule[module].FindRoute(httptest.NewRequest(ex.Method, ex.Path, nil))
			if err != nil {
				if !rejected {
					unmatched = append(unmatched, ex.Method+" "+ex.Path)
				}
				continue
			}
			validated++
			if id, err := openapi.Validate(ctx, docs[module], ex); err != nil {
				k := id + "\x00" + err.Error()
				if failures[k] == nil {
					failures[k] = &failure{operation: id, reason: err.Error(), statuses: map[int]bool{}}
				}
				failures[k].count++
				failures[k].statuses[ex.Status] = true
			}
			key := sampleKey(module, route.Operation.OperationID, ex)
			if counts[key] >= perKey {
				continue
			}
			counts[key]++
			kept[module] = append(kept[module], line)
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
	if len(failures) > 0 {
		return reportFailures(failures, validated)
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
		fmt.Printf("%s: kept %d exchanges\n", name, len(lines))
	}
	fmt.Printf("validated %d raw exchanges\n", validated)
	return nil
}

// failure is one distinct reason an operation's raw exchanges fail.
type failure struct {
	operation, reason string
	statuses          map[int]bool
	count             int
}

// reportFailures prints every distinct failure (operation, statuses, reason
// and how many raw exchanges share it) and returns the error that stops the
// corpus from being written.
func reportFailures(failures map[string]*failure, validated int) error {
	list := make([]*failure, 0, len(failures))
	total := 0
	for _, f := range failures {
		list = append(list, f)
		total += f.count
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].operation != list[j].operation {
			return list[i].operation < list[j].operation
		}
		return list[i].reason < list[j].reason
	})
	for _, f := range list {
		var statuses []string
		for s := range f.statuses {
			statuses = append(statuses, fmt.Sprint(s))
		}
		sort.Strings(statuses)
		fmt.Fprintf(os.Stderr, "%s [%s] ×%d: %s\n", f.operation, strings.Join(statuses, ","), f.count, f.reason)
	}
	return fmt.Errorf("%d of %d raw exchanges do not match the contract (%d distinct failures); nothing written", total, validated, len(list))
}

// sampleKey is what the per-key cap counts: module, operation, status, the
// response content type without parameters, and the response body's shape —
// its sorted top-level keys when it is a JSON object, "-" otherwise. Keying on
// the shape keeps a second body under one status (the antiforgery 400 beside
// a validation-problem 400, say) in the sample.
func sampleKey(module, operationID string, ex openapi.Exchange) string {
	contentType := "-"
	if ex.ResponseContentType != nil {
		contentType, _, _ = strings.Cut(*ex.ResponseContentType, ";")
		contentType = strings.TrimSpace(contentType)
	}
	shape := "-"
	var body map[string]json.RawMessage
	if ex.ResponseBody != nil && json.Unmarshal([]byte(*ex.ResponseBody), &body) == nil && body != nil {
		keys := make([]string, 0, len(body))
		for k := range body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		shape = "{" + strings.Join(keys, ",") + "}"
	}
	return fmt.Sprintf("%s|%s|%d|%s|%s", module, operationID, ex.Status, contentType, shape)
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
		file := filepath.Join(*corpus, name+".jsonl")
		// A module with no corpus file at all has no recorded exchanges, so
		// every one of its operations is uncovered — which is precisely what
		// this report is for. Modules written here rather than ported from
		// .NET (projects is the first) have no file and never will. Any
		// other read failure is a corpus that exists but cannot be read, a
		// broken checkout rather than an absent recording, and still stops
		// the run rather than silently reporting everything uncovered.
		data, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var ex openapi.Exchange
			if err := json.Unmarshal([]byte(line), &ex); err != nil {
				return fmt.Errorf("%s: %w", file, err)
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
