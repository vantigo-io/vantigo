package communications

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// liveWatchedTables are the tables whose rows the inbox or a conversation
// shows: a write to any of them must be heard (live design L18).
var liveWatchedTables = []string{
	"conversations", "conversation_messages", "conversation_tags", "conversation_read_states",
	"conversation_customer_candidates", "conversation_participants",
	"message_deliveries", "message_attachments", "message_events",
}

// liveNotifiers are the calls that notify: live.Notify and this package's
// helpers over it.
var liveNotifiers = map[string]bool{
	"Notify": true, "notifyConversation": true, "notifyConversationChanged": true, "notifyMessage": true,
	"notifyMessageChanged": true, "notifyInbox": true, "notifyOnPool": true,
}

// liveExempt are the functions that write a watched table and deliberately
// notify nobody, each with its reason. An entry that no longer writes fails
// the test, so the list cannot go stale.
var liveExempt = map[string]string{
	"conversations.go:server.PostCommunicationsConversationsByIdRead": "the read state is per user: only the reader's own list shows it, and the reader's client refreshes after its own POST; telling every inbox would refetch it for everyone on each open",
}

// liveWriteQueries are the sqlc queries in queries/*.sql that INSERT, UPDATE
// or DELETE one of the watched tables: the store write methods, by name.
func liveWriteQueries(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("queries/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no queries: %v", err)
	}
	header := regexp.MustCompile(`(?m)^-- name: (\w+) :\w+`)
	comment := regexp.MustCompile(`--[^\n]*`)
	write := regexp.MustCompile(`(?i)(INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+communications\.(\w+)\b`)
	writes := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		heads := header.FindAllStringSubmatchIndex(src, -1)
		for i, h := range heads {
			end := len(src)
			if i+1 < len(heads) {
				end = heads[i+1][0]
			}
			body := comment.ReplaceAllString(src[h[1]:end], "")
			for _, m := range write.FindAllStringSubmatch(body, -1) {
				if slices.Contains(liveWatchedTables, m[2]) {
					writes[src[h[2]:h[3]]] = true
				}
			}
		}
	}
	return writes
}

// liveFunc is one function declaration of the package, as the scan sees it.
type liveFunc struct {
	name     string   // the function's or method's name, what its callers call
	where    string   // file:Recv.Name
	writes   []string // the store write methods it calls
	notifies bool     // it calls a notifier
	calls    map[string]bool
}

// TestLive_EveryWriteIsHeard keeps live design L18's list honest. It finds the
// store write methods on the watched tables from the queries themselves, then
// scans this package's own (non-test) source: every function that calls one
// must notify, itself or through its callers — a function is covered when it
// calls a notifier, or when it has callers in the package and every one of
// them is covered (a helper running on its caller's transaction, such as
// threadInTx inside importMessage). Callers are matched by name, so two
// methods of one name are each held to both sets of callers: stricter, never
// looser. A function no one in the package calls — a handler, a contract's
// method — must notify itself. A new write path that forgets to notify fails
// here, naming the function and the write.
func TestLive_EveryWriteIsHeard(t *testing.T) {
	writes := liveWriteQueries(t)
	for _, known := range []string{"InsertConversation", "InsertConversationMessage", "UpdateConversationFields", "MarkMessageDeliveryFailed", "InsertConversationTagIfAbsent"} {
		if !writes[known] {
			t.Fatalf("the scan of queries/*.sql did not find %s: the scan is broken", known)
		}
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var all []*liveFunc
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			where := f + ":" + fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				where = f + ":" + recvName(fd.Recv.List[0].Type) + "." + fd.Name.Name
			}
			fn := &liveFunc{name: fd.Name.Name, where: where, calls: map[string]bool{}}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var name string
				switch f := call.Fun.(type) {
				case *ast.Ident:
					name = f.Name
				case *ast.SelectorExpr:
					name = f.Sel.Name
				}
				fn.calls[name] = true
				if writes[name] {
					fn.writes = append(fn.writes, name)
				}
				if liveNotifiers[name] {
					fn.notifies = true
				}
				return true
			})
			all = append(all, fn)
		}
	}

	callers := func(name string) []*liveFunc {
		var out []*liveFunc
		for _, fn := range all {
			if fn.calls[name] {
				out = append(out, fn)
			}
		}
		return out
	}
	memo := map[*liveFunc]bool{}
	var covered func(fn *liveFunc, visiting map[*liveFunc]bool) bool
	covered = func(fn *liveFunc, visiting map[*liveFunc]bool) bool {
		if v, ok := memo[fn]; ok {
			return v
		}
		if fn.notifies {
			memo[fn] = true
			return true
		}
		if visiting[fn] {
			return false // a cycle of callers none of which notifies
		}
		visiting[fn] = true
		defer delete(visiting, fn)
		cs := callers(fn.name)
		ok := len(cs) > 0
		for _, c := range cs {
			if c != fn && !covered(c, visiting) {
				ok = false
				break
			}
		}
		memo[fn] = ok
		return ok
	}

	writers := 0
	for _, fn := range all {
		if _, exempt := liveExempt[fn.where]; exempt {
			if len(fn.writes) == 0 {
				t.Errorf("%s is exempt but writes no watched table: drop it from liveExempt", fn.where)
			}
			if fn.notifies {
				t.Errorf("%s is exempt but notifies: drop it from liveExempt", fn.where)
			}
			memo[fn] = true // its callers are not held to it either
			continue
		}
		if len(fn.writes) == 0 {
			continue
		}
		writers++
		if !covered(fn, map[*liveFunc]bool{}) {
			t.Errorf("%s writes %v, and neither it nor every caller of it notifies live (live design L18)", fn.where, fn.writes)
		}
	}
	for where := range liveExempt {
		found := false
		for _, fn := range all {
			found = found || fn.where == where
		}
		if !found {
			t.Errorf("liveExempt names %s, which does not exist: drop it", where)
		}
	}
	if writers < 15 {
		t.Fatalf("the scan found only %d functions that write: the scan is broken", writers)
	}
}

func recvName(e ast.Expr) string {
	switch r := e.(type) {
	case *ast.StarExpr:
		return recvName(r.X)
	case *ast.Ident:
		return r.Name
	case *ast.IndexExpr:
		return recvName(r.X)
	}
	return "?"
}
