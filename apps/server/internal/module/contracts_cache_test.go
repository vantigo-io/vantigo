package module

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// countingLoad is fakeLoad that also says how often each contract was parsed.
func countingLoad(docs map[string]string) (freshContracts, func(string) int64) {
	var mu sync.Mutex
	counts := map[string]*atomic.Int64{}
	counter := func(name string) *atomic.Int64 {
		mu.Lock()
		defer mu.Unlock()
		if counts[name] == nil {
			counts[name] = &atomic.Int64{}
		}
		return counts[name]
	}
	inner := fakeLoad(docs)
	load := func(ctx context.Context, name string) (*openapi3.T, error) {
		counter(name).Add(1)
		return inner(ctx, name)
	}
	return load, func(name string) int64 { return counter(name).Load() }
}

// A test binary composes once per test. The remembered contracts are what
// keeps that from parsing every module's contract every time: the same
// document comes back, and the loader is asked once.
func TestRememberedContracts_ParseEachContractOnce(t *testing.T) {
	t.Parallel()
	load, parsed := countingLoad(map[string]string{"alpha": alphaContract, "beta": betaContract})
	remembered := &rememberedContracts{load: load}
	ctx := context.Background()

	first, err := remembered.doc(ctx, "alpha")
	if err != nil {
		t.Fatalf("doc: %v", err)
	}
	for range 5 {
		again, err := remembered.doc(ctx, "alpha")
		if err != nil {
			t.Fatalf("doc: %v", err)
		}
		if again != first {
			t.Fatal("doc returned a different document the second time; the point is that it is the same one")
		}
	}
	if got := parsed("alpha"); got != 1 {
		t.Errorf("alpha was parsed %d times, want 1", got)
	}
	if got := parsed("beta"); got != 0 {
		t.Errorf("beta was parsed %d times before anyone asked for it, want 0", got)
	}
}

// The combined contract is merged from documents of its own — merging
// rewrites what it is given — and is remembered per combination of modules,
// byte for byte what composing afresh produces.
func TestRememberedContracts_CombineOncePerCombination_AndNeverFromTheSharedDocuments(t *testing.T) {
	t.Parallel()
	fixtures := map[string]string{"alpha": alphaContract, "beta": betaContract}
	load, parsed := countingLoad(fixtures)
	remembered := &rememberedContracts{load: load}
	ctx := context.Background()

	shared, err := remembered.doc(ctx, "alpha")
	if err != nil {
		t.Fatalf("doc: %v", err)
	}
	before, err := shared.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	order := []string{"alpha", "beta"}
	body, err := remembered.combined(ctx, order)
	if err != nil {
		t.Fatalf("combined: %v", err)
	}
	fresh, err := freshContracts(fakeLoad(fixtures)).combined(ctx, order)
	if err != nil {
		t.Fatalf("fresh combined: %v", err)
	}
	if !bytes.Equal(body, fresh) {
		t.Error("the remembered combined contract differs from one composed afresh")
	}

	parsedAfterFirst := parsed("alpha") + parsed("beta")
	if _, err := remembered.combined(ctx, order); err != nil {
		t.Fatalf("combined again: %v", err)
	}
	if got := parsed("alpha") + parsed("beta"); got != parsedAfterFirst {
		t.Errorf("combining the same modules again parsed %d more contracts, want 0", got-parsedAfterFirst)
	}
	if _, err := remembered.combined(ctx, []string{"alpha"}); err != nil {
		t.Fatalf("combined alpha only: %v", err)
	}
	if got := parsed("alpha") + parsed("beta"); got == parsedAfterFirst {
		t.Error("another combination of modules was served from the first one's entry")
	}

	after, err := shared.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("combining changed the shared document that modules were handed")
	}
}

// Many tests start at once; they must all end up with one document.
func TestRememberedContracts_ConcurrentFirstCallersShareOneDocument(t *testing.T) {
	t.Parallel()
	load, _ := countingLoad(map[string]string{"alpha": alphaContract})
	remembered := &rememberedContracts{load: load}

	const callers = 16
	docs := make([]*openapi3.T, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc, err := remembered.doc(context.Background(), "alpha")
			if err != nil {
				t.Errorf("doc: %v", err)
				return
			}
			docs[i] = doc
		}()
	}
	wg.Wait()
	for i, doc := range docs {
		if doc != docs[0] {
			t.Fatalf("caller %d got a document of its own", i)
		}
	}
}

// The three tests above hold for rememberedContracts; this one holds for
// Compose. Composing twice must hand a module the very same document and
// serve the very same combined contract — without it, Compose could go back
// to parsing per composition and every other test here would stay green
// while the suite went back to seventeen minutes.
func TestCompose_RemembersTheEmbeddedContractsBetweenCompositions(t *testing.T) {
	compose := func() (*openapi3.T, []byte) {
		t.Helper()
		var doc *openapi3.T
		mods := []Module{{Name: "identity", Mount: func(d Deps) (http.Handler, error) {
			doc = d.Doc
			return http.NotFoundHandler(), nil
		}}}
		handler, err := Compose(Deps{Access: &fakeAccess{}, Config: &config.Config{}}, mods...)
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/openapi.json = %d, want 200", rec.Code)
		}
		body, err := io.ReadAll(rec.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return doc, body
	}

	firstDoc, firstBody := compose()
	secondDoc, secondBody := compose()
	if firstDoc == nil || firstDoc != secondDoc {
		t.Error("two compositions were handed different documents; Compose is parsing per composition again")
	}
	if len(firstBody) == 0 || !bytes.Equal(firstBody, secondBody) {
		t.Error("two compositions of the same modules serve different combined contracts")
	}
}

// A contract that failed to load is not remembered: the next composition
// asks again rather than failing forever.
func TestRememberedContracts_DoNotRememberAFailure(t *testing.T) {
	t.Parallel()
	inner := fakeLoad(map[string]string{"alpha": alphaContract})
	var failed atomic.Bool
	remembered := &rememberedContracts{load: func(ctx context.Context, name string) (*openapi3.T, error) {
		if failed.CompareAndSwap(false, true) {
			return nil, errors.New("the first load fails")
		}
		return inner(ctx, name)
	}}
	ctx := context.Background()

	if _, err := remembered.doc(ctx, "alpha"); err == nil {
		t.Fatal("the failing load succeeded")
	}
	if _, err := remembered.doc(ctx, "alpha"); err != nil {
		t.Errorf("the load after a failure: %v, want it to be asked again and succeed", err)
	}
	if _, err := remembered.combined(ctx, []string{"alpha"}); err != nil {
		t.Errorf("combined after a failure: %v", err)
	}
}
