package gen

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// recordingMux captures the patterns the generated server registers. The
// stdlib http.ServeMux cannot hold every route of some modules' contracts
// (see openapi.TestServeMuxConflictsArePinned); mounting on a
// precedence-aware router is sub-project 3's job. Recording what gets
// registered — instead of mounting on a real ServeMux — lets this test prove
// the generated wiring independently of that.
type recordingMux struct{ patterns []string }

func (m *recordingMux) HandleFunc(pattern string, _ func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
}
func (m *recordingMux) ServeHTTP(http.ResponseWriter, *http.Request) {}

// The generated strict server mounts exactly the contract's operations.
func TestGeneratedServerMountsEveryOperation(t *testing.T) {
	mux := &recordingMux{}
	var server StrictServerInterface // nil: only the wiring is under test
	HandlerWithOptions(NewStrictHandler(server, nil), StdHTTPServerOptions{BaseRouter: mux})

	doc, err := openapi.Load(context.Background(), "products")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			want = append(want, method+" "+path)
		}
	}
	sort.Strings(want)

	got := append([]string(nil), mux.patterns...)
	sort.Strings(got)

	if !slices.Equal(got, want) {
		t.Errorf("mounted patterns don't match the contract:\nmounted:  %v\ncontract: %v", got, want)
	}
}
