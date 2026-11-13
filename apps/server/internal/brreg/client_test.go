package brreg_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/brreg"
)

// The search, against a local registry: the request it makes, the matches it
// answers, and the failures it reports. Nothing here opens a real socket to
// Brønnøysund or sleeps a real backoff.

func noBackoff(int) time.Duration { return 0 }

// registry is a local Enhetsregisteret answering handler.
func registry(t *testing.T, handler http.HandlerFunc) *brreg.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return brreg.New(srv.URL, 5*time.Second, nil, noBackoff)
}

func TestSearch_ByOrganisationNumberAndByName(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var paths []string
	c := registry(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_embedded":{"enheter":[{"organisasjonsnummer":"923609016","navn":"EQUINOR ASA"}]}}`))
	})
	got, err := c.Search(t.Context(), brreg.SearchQuery{OrganisationNumber: "923609016", Name: "ignored", Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (brreg.Match{OrganisationNumber: "923609016", Name: "EQUINOR ASA"}) {
		t.Errorf("matches = %+v, want Equinor", got)
	}
	if _, err := c.Search(t.Context(), brreg.SearchQuery{Name: "equi nor", Size: 3}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/enhetsregisteret/api/enheter?organisasjonsnummer=923609016&size=10",
		"/enhetsregisteret/api/enheter?navn=equi+nor&size=3",
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("requests = %v, want %v", paths, want)
	}
}

func TestSearch_NoEmbeddedIsNoMatches(t *testing.T) {
	t.Parallel()
	c := registry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"page":{"totalElements":0}}`))
	})
	got, err := c.Search(t.Context(), brreg.SearchQuery{Name: "nobody", Size: 10})
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("Search = %v, %v; want an empty list and no error", got, err)
	}
}

func TestSearch_RetriesA5xxThenAnswers(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	c := registry(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"_embedded":{"enheter":[{"organisasjonsnummer":"974760673","navn":"REGISTERENHETEN I BRØNNØYSUND"}]}}`))
	})
	got, err := c.Search(t.Context(), brreg.SearchQuery{OrganisationNumber: "974760673", Size: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("Search = %v, %v; want the one match after two retries", got, err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("%d attempts, want 3", n)
	}
}

func TestSearch_Failures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		status    int
		body      string
		undecoded bool
		wantCalls int32
	}{
		{name: "a 404 is not retried", status: http.StatusNotFound, body: `{}`, wantCalls: 1},
		{name: "a 500 is retried until the attempts run out", status: http.StatusInternalServerError, body: `{}`, wantCalls: 4},
		{name: "a 2xx that will not decode", status: http.StatusOK, body: `not json`, undecoded: true, wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			c := registry(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.Search(t.Context(), brreg.SearchQuery{Name: "acme", Size: 10})
			if !errors.Is(err, brreg.ErrUnavailable) {
				t.Fatalf("error = %v, want it to wrap ErrUnavailable", err)
			}
			if got := errors.Is(err, brreg.ErrUndecodable); got != tc.undecoded {
				t.Errorf("errors.Is(err, ErrUndecodable) = %v, want %v", got, tc.undecoded)
			}
			if n := calls.Load(); n != tc.wantCalls {
				t.Errorf("%d attempts, want %d", n, tc.wantCalls)
			}
		})
	}
}
